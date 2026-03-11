package notify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hnl1/xgydeploy/internal/config"
	"github.com/hnl1/xgydeploy/internal/scheduler"
	"github.com/hnl1/xgydeploy/internal/xgc"
)

func formatRule(rule config.ScheduleRule) string {
	if rule.MinCount != nil {
		return fmt.Sprintf("最少 %d 个", *rule.MinCount)
	}
	if rule.MaxCount != nil {
		return fmt.Sprintf("最多 %d 个", *rule.MaxCount)
	}
	return ""
}

func formatBalance(balance float64) string {
	return fmt.Sprintf("💰 账户余额：%.2f 元\n", balance)
}

func formatModelDetail(detail map[string]int) string {
	if len(detail) == 0 {
		return ""
	}
	keys := make([]string, 0, len(detail))
	for k := range detail {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s×%d", k, detail[k]))
	}
	return strings.Join(parts, " / ")
}

func buildPlanMessage(plans []scheduler.ActionPlan, timeStr string, balance float64) string {
	var sb strings.Builder
	sb.WriteString("## 【仙宫云调度】准备执行\n\n")
	sb.WriteString("⏰ 时间：" + timeStr + "\n\n")
	sb.WriteString(formatBalance(balance) + "\n")

	for _, p := range plans {
		target := formatRule(p.Rule)
		preferred := xgc.GPUModelShortName(p.DeployOpts.GPUModel)
		sb.WriteString(fmt.Sprintf("### %s\n", p.ConfigKey))
		sb.WriteString(fmt.Sprintf("- 规则：%s %s\n", p.Rule.Time, target))
		sb.WriteString(fmt.Sprintf("- 首选型号：%s\n", preferred))
		sb.WriteString(fmt.Sprintf("- 当前实例：%d 个（首选 %d / 回退 %d）\n", p.Current, p.PreferredCount, p.FallbackCount))
		if len(p.FallbackDetail) > 0 {
			sb.WriteString(fmt.Sprintf("- 回退分布：%s\n", formatModelDetail(p.FallbackDetail)))
		}

		switch p.Action {
		case "create":
			if p.Count > 0 {
				sb.WriteString(fmt.Sprintf("- 📥 计划创建：%d 个（允许回退）\n", p.Count))
			} else {
				sb.WriteString("- ✅ 无需操作，已满足要求\n")
			}
		case "destroy":
			if p.Count > 0 {
				sb.WriteString(fmt.Sprintf("- 📤 计划销毁：%d 个\n", p.Count))
				for _, id := range p.DestroyIDs {
					sb.WriteString(fmt.Sprintf("  - `%s`\n", id))
				}
			} else {
				sb.WriteString("- ✅ 无需操作，已满足要求\n")
			}
		case "replace":
			sb.WriteString(fmt.Sprintf("- 🔄 计划替换：%d 个 → %s\n", p.Count, preferred))
			for _, id := range p.DestroyIDs {
				sb.WriteString(fmt.Sprintf("  - `%s`\n", id))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func buildResultMessage(results []scheduler.ActionResult, timeStr string, balance float64) string {
	var sb strings.Builder
	sb.WriteString("## 【仙宫云调度】执行完成\n\n")
	sb.WriteString("⏰ 执行时间：" + timeStr + "\n\n")
	sb.WriteString(formatBalance(balance) + "\n")

	for _, r := range results {
		target := formatRule(r.Rule)
		status := "✅ 成功"
		if !r.Success {
			status = "❌ 失败"
		}
		sb.WriteString(fmt.Sprintf("### %s | %s\n", r.ConfigKey, status))
		sb.WriteString(fmt.Sprintf("- 规则：%s %s\n", r.Rule.Time, target))
		if r.PlannedGPUModel != "" {
			sb.WriteString(fmt.Sprintf("- 计划型号：%s\n", r.PlannedGPUModel))
		}
		sb.WriteString(fmt.Sprintf("- 操作前：%d 个\n", r.BeforeCount))
		sb.WriteString(fmt.Sprintf("- 操作后：%d 个\n", r.AfterCount))
		if len(r.Created) > 0 {
			sb.WriteString(fmt.Sprintf("- 已创建：%d 个\n", len(r.Created)))
			if len(r.ActualGPUModels) > 0 {
				sb.WriteString(fmt.Sprintf("- 实际型号：%s\n", formatModelDetail(r.ActualGPUModels)))
			}
			for _, id := range r.Created {
				sb.WriteString(fmt.Sprintf("  - `%s`\n", id))
			}
		}
		if r.Replaced > 0 {
			sb.WriteString(fmt.Sprintf("- 🔄 已替换：%d 个\n", r.Replaced))
		}
		if len(r.Destroyed) > 0 {
			sb.WriteString(fmt.Sprintf("- 已销毁：%d 个\n", len(r.Destroyed)))
			for _, id := range r.Destroyed {
				sb.WriteString(fmt.Sprintf("  - `%s`\n", id))
			}
		}
		if len(r.Errors) > 0 {
			sb.WriteString("- 错误：\n")
			for _, e := range r.Errors {
				sb.WriteString(fmt.Sprintf("  - %s（%d 个）\n", e.Message, e.Count))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func sendDingtalk(title, text string) bool {
	webhook := os.Getenv("DINGTALK_WEBHOOK")
	if webhook == "" {
		return false
	}
	if secret := os.Getenv("DINGTALK_SECRET"); secret != "" {
		var err error
		webhook, err = signWebhookURL(webhook, secret)
		if err != nil {
			return false
		}
	}
	payload := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": title,
			"text":  text,
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func SendPlanDingtalk(plans []scheduler.ActionPlan, timeStr string, balance float64) bool {
	return sendDingtalk("仙宫云调度 - 准备执行", buildPlanMessage(plans, timeStr, balance))
}

func SendResultDingtalk(results []scheduler.ActionResult, timeStr string, balance float64) bool {
	return sendDingtalk("仙宫云调度 - 执行完成", buildResultMessage(results, timeStr, balance))
}

func SendConfigDingtalk(rawYAML string) bool {
	content := "【仙宫云调度】当前配置\n\n" + rawYAML
	webhook := os.Getenv("DINGTALK_WEBHOOK")
	if webhook == "" {
		return false
	}
	if secret := os.Getenv("DINGTALK_SECRET"); secret != "" {
		var err error
		webhook, err = signWebhookURL(webhook, secret)
		if err != nil {
			return false
		}
	}
	payload := map[string]any{
		"msgtype": "text",
		"text": map[string]string{
			"content": content,
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func signWebhookURL(webhook, secret string) (string, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	stringToSign := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stringToSign))
	sign := base64.StdEncoding.EncodeToString(h.Sum(nil))
	sign = url.QueryEscape(sign)
	sep := "?"
	if strings.Contains(webhook, "?") {
		sep = "&"
	}
	return webhook + sep + "timestamp=" + timestamp + "&sign=" + sign, nil
}
