package notify

import (
	"strings"
	"testing"

	"github.com/hnl1/xgydeploy/internal/config"
	"github.com/hnl1/xgydeploy/internal/scheduler"
	"github.com/hnl1/xgydeploy/internal/xgc"
)

func intPtr(n int) *int { return &n }

// --- formatRule ---

func TestFormatRule(t *testing.T) {
	tests := []struct {
		name string
		rule config.ScheduleRule
		want string
	}{
		{"min_count", config.ScheduleRule{MinCount: intPtr(3)}, "最少 3 个"},
		{"max_count", config.ScheduleRule{MaxCount: intPtr(0)}, "最多 0 个"},
		{"neither", config.ScheduleRule{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRule(tt.rule); got != tt.want {
				t.Errorf("formatRule() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- formatBalance ---

func TestFormatBalance(t *testing.T) {
	got := formatBalance(123.456)
	if !strings.Contains(got, "123.46") {
		t.Errorf("formatBalance(123.456) = %q, want to contain %q", got, "123.46")
	}
	if !strings.Contains(got, "💰") {
		t.Errorf("formatBalance() should contain balance emoji")
	}
}

// --- actionEmoji ---

func TestActionEmoji(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{"create", "📥"},
		{"destroy", "🗑️"},
		{"replace", "🔄"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		if got := actionEmoji(tt.action); got != tt.want {
			t.Errorf("actionEmoji(%q) = %q, want %q", tt.action, got, tt.want)
		}
	}
}

// --- formatPlanAction ---

func TestFormatPlanAction(t *testing.T) {
	tests := []struct {
		plan scheduler.ActionPlan
		want string
	}{
		{scheduler.ActionPlan{Action: "create", Count: 3}, "📥 计划创建 3 个"},
		{scheduler.ActionPlan{Action: "destroy", Count: 2}, "🗑️ 计划销毁 2 个"},
		{scheduler.ActionPlan{Action: "replace", Count: 1}, "🔄 计划替换 1 个"},
		{scheduler.ActionPlan{Action: "unknown"}, "未知"},
	}
	for _, tt := range tests {
		if got := formatPlanAction(tt.plan); got != tt.want {
			t.Errorf("formatPlanAction(%q) = %q, want %q", tt.plan.Action, got, tt.want)
		}
	}
}

// --- resultActionWord ---

func TestResultActionWord(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{"create", "创建"},
		{"destroy", "销毁"},
		{"replace", "替换"},
		{"other", "操作"},
	}
	for _, tt := range tests {
		if got := resultActionWord(tt.action); got != tt.want {
			t.Errorf("resultActionWord(%q) = %q, want %q", tt.action, got, tt.want)
		}
	}
}

// --- fullModelDist ---

func TestFullModelDist(t *testing.T) {
	dist := fullModelDist("RTX 4090", 3, map[string]int{"RTX 4090 D": 2})
	if dist["RTX 4090"] != 3 {
		t.Errorf("dist[RTX 4090] = %d, want 3", dist["RTX 4090"])
	}
	if dist["RTX 4090 D"] != 2 {
		t.Errorf("dist[RTX 4090 D] = %d, want 2", dist["RTX 4090 D"])
	}
}

func TestFullModelDistNoPreferred(t *testing.T) {
	dist := fullModelDist("RTX 4090", 0, map[string]int{"RTX 4090 D": 1})
	if _, ok := dist["RTX 4090"]; ok {
		t.Error("dist should not contain preferred model with 0 count")
	}
	if dist["RTX 4090 D"] != 1 {
		t.Errorf("dist[RTX 4090 D] = %d, want 1", dist["RTX 4090 D"])
	}
}

// --- writeModelDist ---

func TestWriteModelDist(t *testing.T) {
	dist := map[string]int{"RTX 4090": 3, "RTX 4090 D": 2, "RTX 4090 48G": 0}
	var sb strings.Builder
	writeModelDist(&sb, dist)
	out := sb.String()

	if !strings.Contains(out, "RTX 4090 × 3") {
		t.Errorf("output should contain 'RTX 4090 × 3', got: %q", out)
	}
	if !strings.Contains(out, "RTX 4090 D × 2") {
		t.Errorf("output should contain 'RTX 4090 D × 2', got: %q", out)
	}
	if strings.Contains(out, "48G") {
		t.Errorf("output should not contain model with 0 count, got: %q", out)
	}
}

// --- buildResultMessage ---

func TestBuildResultMessage(t *testing.T) {
	plans := []scheduler.ActionPlan{
		{
			ConfigKey:      "test-image",
			ImageID:        "img-001",
			Rule:           config.ScheduleRule{Time: "08:00", MinCount: intPtr(2)},
			Current:        1,
			PreferredCount: 1,
			FallbackCount:  0,
			FallbackDetail: map[string]int{},
			Action:         "create",
			Count:          1,
			DeployOpts:     xgc.DeployOpts{GPUModel: "NVIDIA GeForce RTX 4090"},
		},
	}
	results := []scheduler.ActionResult{
		{
			ConfigKey:   "test-image",
			ImageID:     "img-001",
			Rule:        config.ScheduleRule{Time: "08:00", MinCount: intPtr(2)},
			Action:      "create",
			BeforeCount: 1,
			AfterCount:  2,
			CreatedInstances: []scheduler.InstanceRef{
				{ID: "new-1", GPUModel: "RTX 4090"},
			},
			PlannedGPUModel: "RTX 4090",
			Success:         true,
		},
	}

	msg := buildResultMessage(plans, results, "2025-06-15 10:00", 500.00)

	checks := []string{
		"执行报告",
		"2025-06-15 10:00",
		"500.00",
		"test-image",
		"创建",
		"new-1",
	}
	for _, c := range checks {
		if !strings.Contains(msg, c) {
			t.Errorf("message should contain %q\n  full message:\n%s", c, msg)
		}
	}
}

func TestBuildResultMessageDestroy(t *testing.T) {
	plans := []scheduler.ActionPlan{
		{
			ConfigKey:      "prod-app",
			ImageID:        "img-002",
			Rule:           config.ScheduleRule{Time: "20:00", MaxCount: intPtr(0)},
			Current:        3,
			PreferredCount: 2,
			FallbackCount:  1,
			FallbackDetail: map[string]int{"RTX 4090 D": 1},
			Action:         "destroy",
			Count:          3,
			DeployOpts:     xgc.DeployOpts{GPUModel: "NVIDIA GeForce RTX 4090"},
		},
	}
	results := []scheduler.ActionResult{
		{
			ConfigKey:   "prod-app",
			ImageID:     "img-002",
			Rule:        config.ScheduleRule{Time: "20:00", MaxCount: intPtr(0)},
			Action:      "destroy",
			BeforeCount: 3,
			AfterCount:  0,
			DestroyedInstances: []scheduler.InstanceRef{
				{ID: "old-1", GPUModel: "RTX 4090"},
				{ID: "old-2", GPUModel: "RTX 4090"},
				{ID: "old-3", GPUModel: "RTX 4090 D"},
			},
			PlannedGPUModel: "RTX 4090",
			Success:         true,
		},
	}

	msg := buildResultMessage(plans, results, "2025-06-15 20:13", 800.00)

	checks := []string{
		"prod-app",
		"销毁",
		"old-1",
		"old-3",
		"20:00",
		"最多 0 个",
		"备选型号 1",
	}
	for _, c := range checks {
		if !strings.Contains(msg, c) {
			t.Errorf("destroy message should contain %q\n  full message:\n%s", c, msg)
		}
	}
}

func TestBuildResultMessageReplace(t *testing.T) {
	plans := []scheduler.ActionPlan{
		{
			ConfigKey:      "staging",
			ImageID:        "img-003",
			Rule:           config.ScheduleRule{Time: "08:00", MinCount: intPtr(2)},
			Current:        2,
			PreferredCount: 1,
			FallbackCount:  1,
			FallbackDetail: map[string]int{"RTX 4090 D": 1},
			Action:         "replace",
			Count:          1,
			DeployOpts:     xgc.DeployOpts{GPUModel: "NVIDIA GeForce RTX 4090"},
		},
	}
	results := []scheduler.ActionResult{
		{
			ConfigKey:   "staging",
			ImageID:     "img-003",
			Rule:        config.ScheduleRule{Time: "08:00", MinCount: intPtr(2)},
			Action:      "replace",
			BeforeCount: 2,
			AfterCount:  2,
			CreatedInstances: []scheduler.InstanceRef{
				{ID: "new-r1", GPUModel: "RTX 4090"},
			},
			DestroyedInstances: []scheduler.InstanceRef{
				{ID: "old-r1", GPUModel: "RTX 4090 D"},
			},
			PlannedGPUModel: "RTX 4090",
			Replaced:        1,
			Success:         true,
		},
	}

	msg := buildResultMessage(plans, results, "2025-06-15 08:13", 600.00)

	checks := []string{
		"staging",
		"替换",
		"new-r1",
		"old-r1",
		"新增",
		"销毁",
	}
	for _, c := range checks {
		if !strings.Contains(msg, c) {
			t.Errorf("replace message should contain %q\n  full message:\n%s", c, msg)
		}
	}
}

func TestBuildResultMessageWithErrors(t *testing.T) {
	plans := []scheduler.ActionPlan{
		{
			ConfigKey:      "err-test",
			ImageID:        "img-err",
			Rule:           config.ScheduleRule{Time: "08:00", MinCount: intPtr(3)},
			Current:        0,
			PreferredCount: 0,
			FallbackCount:  0,
			FallbackDetail: map[string]int{},
			Action:         "create",
			Count:          3,
			DeployOpts:     xgc.DeployOpts{GPUModel: "NVIDIA GeForce RTX 4090"},
		},
	}
	results := []scheduler.ActionResult{
		{
			ConfigKey:        "err-test",
			ImageID:          "img-err",
			Rule:             config.ScheduleRule{Time: "08:00", MinCount: intPtr(3)},
			Action:           "create",
			BeforeCount:      0,
			AfterCount:       1,
			CreatedInstances: []scheduler.InstanceRef{{ID: "ok-1", GPUModel: "RTX 4090"}},
			PlannedGPUModel:  "RTX 4090",
			Success:          false,
			Errors: []scheduler.ErrCount{
				{Message: "可用GPU不足", Count: 2},
			},
		},
	}

	msg := buildResultMessage(plans, results, "2025-06-15 08:13", 100.00)

	checks := []string{
		"⚠️ 错误",
		"可用GPU不足",
		"2 个",
	}
	for _, c := range checks {
		if !strings.Contains(msg, c) {
			t.Errorf("error message should contain %q\n  full message:\n%s", c, msg)
		}
	}
}

// --- signWebhookURL ---

func TestSignWebhookURL(t *testing.T) {
	webhook := "https://oapi.dingtalk.com/robot/send?access_token=test"
	secret := "SEC123456"

	signed, err := signWebhookURL(webhook, secret)
	if err != nil {
		t.Fatalf("signWebhookURL() error: %v", err)
	}
	if !strings.Contains(signed, "timestamp=") {
		t.Errorf("signed URL should contain timestamp=, got: %q", signed)
	}
	if !strings.Contains(signed, "sign=") {
		t.Errorf("signed URL should contain sign=, got: %q", signed)
	}
	if !strings.HasPrefix(signed, webhook+"&") {
		t.Errorf("signed URL should start with original webhook + &, got: %q", signed)
	}
}

func TestSignWebhookURLNoQueryString(t *testing.T) {
	webhook := "https://example.com/webhook"
	signed, err := signWebhookURL(webhook, "secret")
	if err != nil {
		t.Fatalf("signWebhookURL() error: %v", err)
	}
	if !strings.HasPrefix(signed, webhook+"?") {
		t.Errorf("signed URL without existing query should use '?', got: %q", signed)
	}
}
