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
	"strconv"
	"strings"
	"time"

	"github.com/hnl1/xgydeploy/internal/config"
	"github.com/hnl1/xgydeploy/internal/scheduler"
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

func buildPlanMessage(plans []scheduler.ActionPlan, timeStr string, balance float64) string {
	var sb strings.Builder
	sb.WriteString("## 【仙宫云调度】准备执行\n\n")
	sb.WriteString("⏰ 时间：" + timeStr + "\n\n")
	sb.WriteString(formatBalance(balance) + "\n")

	for _, p := range plans {
		target := formatRule(p.Rule)
		sb.WriteString(fmt.Sprintf("### %s\n", p.ConfigKey))
		sb.WriteString(fmt.Sprintf("- 规则：%s %s\n", p.Rule.Time, target))
		sb.WriteString(fmt.Sprintf("- 当前实例：%d 个\n", p.Current))

		switch p.Action {
		case "create":
			if p.Count > 0 {
				sb.WriteString(fmt.Sprintf("- 📥 计划创建：%d 个\n", p.Count))
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
		sb.WriteString(fmt.Sprintf("- 操作前：%d 个\n", r.BeforeCount))
		sb.WriteString(fmt.Sprintf("- 操作后：%d 个\n", r.AfterCount))
		if len(r.Created) > 0 {
			sb.WriteString(fmt.Sprintf("- 已创建：%d 个\n", len(r.Created)))
			for _, id := range r.Created {
				sb.WriteString(fmt.Sprintf("  - `%s`\n", id))
			}
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
