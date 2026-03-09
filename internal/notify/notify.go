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

func buildMessage(results []scheduler.ActionResult, timeStr string) string {
	var sb strings.Builder
	sb.WriteString("## 【仙宫云调度】执行完成\n\n")
	sb.WriteString("⏰ 执行时间：" + timeStr + "\n\n")
	for _, r := range results {
		target := formatRule(r.Rule)
		status := "✅ 成功"
		if !r.Success {
			status = "❌ 失败"
		}
		sb.WriteString(fmt.Sprintf("### 配置 %s | %s\n", r.ConfigKey, status))
		imgShort := r.ImageID
		if len(imgShort) > 8 {
			imgShort = imgShort[:8] + "..."
		}
		sb.WriteString(fmt.Sprintf("- 镜像：`%s`\n", imgShort))
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
		if r.Error != "" {
			sb.WriteString("- 错误：" + r.Error + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func SendDingtalk(results []scheduler.ActionResult, timeStr string) bool {
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
	text := buildMessage(results, timeStr)
	payload := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": "仙宫云调度",
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

// signWebhookURL 钉钉 webhook 加签：timestamp + "\n" + secret，HMAC-SHA256，Base64，URL 编码
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
