package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

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
		}
		if len(r.Destroyed) > 0 {
			sb.WriteString(fmt.Sprintf("- 已销毁：%d 个\n", len(r.Destroyed)))
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
