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

func formatPlanAction(p scheduler.ActionPlan) string {
	preferred := xgc.GPUModelShortName(p.DeployOpts.GPUModel)
	switch p.Action {
	case "create":
		return fmt.Sprintf("📥 创建 %d 个（允许回退）", p.Count)
	case "destroy":
		return fmt.Sprintf("📤 销毁 %d 个", p.Count)
	case "replace":
		return fmt.Sprintf("🔄 替换 %d 个 → %s", p.Count, preferred)
	}
	return "未知"
}

func buildResultMessage(plans []scheduler.ActionPlan, results []scheduler.ActionResult, timeStr string, balance float64) string {
	var sb strings.Builder
	sb.WriteString("## 【仙宫云调度】执行报告\n\n")
	sb.WriteString("⏰ 时间：" + timeStr + "\n\n")
	sb.WriteString(formatBalance(balance) + "\n")

	for i, r := range results {
		status := "✅ 成功"
		if !r.Success {
			status = "❌ 失败"
		}
		sb.WriteString(fmt.Sprintf("### %s | %s\n\n", r.ConfigKey, status))

		// 计划情况
		if i < len(plans) {
			p := plans[i]
			target := formatRule(p.Rule)
			preferred := xgc.GPUModelShortName(p.DeployOpts.GPUModel)
			sb.WriteString("**计划情况**\n")
			sb.WriteString(fmt.Sprintf("- 规则：%s %s\n", p.Rule.Time, target))
			sb.WriteString(fmt.Sprintf("- 首选型号：%s\n", preferred))
			sb.WriteString(fmt.Sprintf("- 执行前实例：%d 个（首选 %d / 回退 %d）\n", p.Current, p.PreferredCount, p.FallbackCount))
			if len(p.FallbackDetail) > 0 {
				sb.WriteString(fmt.Sprintf("- 回退分布：%s\n", formatModelDetail(p.FallbackDetail)))
			}
			sb.WriteString(fmt.Sprintf("- 计划操作：%s\n", formatPlanAction(p)))
			sb.WriteString("\n")
		}

		// 实际执行情况
		sb.WriteString("**执行结果**\n")
		sb.WriteString(fmt.Sprintf("- 执行后实例：%d 个\n", r.AfterCount))
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
			sb.WriteString(fmt.Sprintf("- 已替换：%d 个\n", r.Replaced))
		}
		if len(r.Destroyed) > 0 {
			sb.WriteString(fmt.Sprintf("- 已销毁：%d 个\n", len(r.Destroyed)))
			for _, id := range r.Destroyed {
				sb.WriteString(fmt.Sprintf("  - `%s`\n", id))
			}
		}
		if len(r.Errors) > 0 {
			sb.WriteString("- ⚠️ 错误：\n")
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

func SendResultDingtalk(plans []scheduler.ActionPlan, results []scheduler.ActionResult, timeStr string, balance float64) bool {
	return sendDingtalk("仙宫云调度 - 执行报告", buildResultMessage(plans, results, timeStr, balance))
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
