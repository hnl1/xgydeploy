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

func actionEmoji(action string) string {
	switch action {
	case "create":
		return "📥"
	case "destroy":
		return "🗑️"
	case "replace":
		return "🔄"
	}
	return ""
}

func formatPlanAction(p scheduler.ActionPlan) string {
	switch p.Action {
	case "create":
		return fmt.Sprintf("📥 计划创建 %d 个", p.Count)
	case "destroy":
		return fmt.Sprintf("🗑️ 计划销毁 %d 个", p.Count)
	case "replace":
		return fmt.Sprintf("🔄 计划替换 %d 个", p.Count)
	}
	return "未知"
}

func fullModelDist(preferredModel string, preferredCount int, fallbackDetail map[string]int) map[string]int {
	dist := make(map[string]int)
	if preferredCount > 0 {
		dist[preferredModel] = preferredCount
	}
	for m, c := range fallbackDetail {
		dist[m] = c
	}
	return dist
}

func writeModelDist(sb *strings.Builder, dist map[string]int) {
	keys := make([]string, 0, len(dist))
	for k := range dist {
		if dist[k] > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(sb, "- %s × %d\n", k, dist[k])
	}
}

func resultActionWord(action string) string {
	switch action {
	case "create":
		return "创建"
	case "destroy":
		return "销毁"
	case "replace":
		return "替换"
	}
	return "操作"
}

func buildResultMessage(plans []scheduler.ActionPlan, results []scheduler.ActionResult, timeStr string, balance float64) string {
	var sb strings.Builder
	sb.WriteString("## 【仙宫云调度】执行报告\n\n")
	sb.WriteString("⏰ 时间：" + timeStr + "\n\n")
	sb.WriteString(formatBalance(balance) + "\n")

	for i, r := range results {
		actionWord := resultActionWord(r.Action)
		actualCount := len(r.CreatedInstances)
		switch r.Action {
		case "destroy":
			actualCount = len(r.DestroyedInstances)
		case "replace":
			actualCount = r.Replaced
		}

		fmt.Fprintf(&sb, "### %s | %s %s %d 个\n\n", r.ConfigKey, actionEmoji(r.Action), actionWord, actualCount)

		var preferred string
		var beforeDist map[string]int

		if i < len(plans) {
			p := plans[i]
			preferred = xgc.GPUModelShortName(p.DeployOpts.GPUModel)
			beforeDist = fullModelDist(preferred, p.PreferredCount, p.FallbackDetail)

			// 规则
			fmt.Fprintf(&sb, "**规则**：%s %s，首选 %s\n\n", p.Rule.Time, formatRule(p.Rule), preferred)

			// 执行前实例
			fmt.Fprintf(&sb, "**执行前实例共 %d 个（含回退 %d）**\n", p.Current, p.FallbackCount)
			writeModelDist(&sb, beforeDist)
			sb.WriteString("\n")

			// 计划 + 实际结果
			fmt.Fprintf(&sb, "**%s，实际成功%s %d 个**\n", formatPlanAction(p), actionWord, actualCount)
		}

		for _, ref := range r.CreatedInstances {
			fmt.Fprintf(&sb, "- 新增 %s %s\n", ref.GPUModel, ref.ID)
		}
		for _, ref := range r.DestroyedInstances {
			fmt.Fprintf(&sb, "- 销毁 %s %s\n", ref.GPUModel, ref.ID)
		}

		if len(r.Errors) > 0 {
			sb.WriteString("\n**⚠️ 错误**\n")
			for _, e := range r.Errors {
				fmt.Fprintf(&sb, "- %s（%d 个）\n", e.Message, e.Count)
			}
		}

		// 目前实例
		if beforeDist != nil {
			afterDist := make(map[string]int)
			for k, v := range beforeDist {
				afterDist[k] = v
			}
			for _, ref := range r.CreatedInstances {
				afterDist[ref.GPUModel]++
			}
			for _, ref := range r.DestroyedInstances {
				afterDist[ref.GPUModel]--
				if afterDist[ref.GPUModel] <= 0 {
					delete(afterDist, ref.GPUModel)
				}
			}
			afterTotal := 0
			afterPreferred := 0
			for m, c := range afterDist {
				afterTotal += c
				if m == preferred {
					afterPreferred += c
				}
			}
			fmt.Fprintf(&sb, "\n**目前实例共 %d 个（含回退 %d）**\n", afterTotal, afterTotal-afterPreferred)
			writeModelDist(&sb, afterDist)
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
	defer resp.Body.Close() //nolint:errcheck
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
	defer resp.Body.Close() //nolint:errcheck
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
