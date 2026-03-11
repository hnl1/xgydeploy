package scheduler

import (
	"errors"
	"testing"
	"time"

	"github.com/hnl1/xgydeploy/internal/config"
	"github.com/hnl1/xgydeploy/internal/xgc"
)

// --- mock implementations ---

type mockPlanClient struct {
	instances []map[string]any
	images    map[string]string
	listErr   error
}

func (m *mockPlanClient) ListInstances() ([]map[string]any, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.instances, nil
}

func (m *mockPlanClient) ListImages() (map[string]string, error) {
	return m.images, nil
}

type mockExecClient struct {
	deployResults []xgc.DeployResult
	deployErrs    []error
	destroyedIDs  []string
	destroyErrs   []error
	runningIDs    []string
}

func (m *mockExecClient) DeployAsync(opts xgc.DeployOpts, count int, allowFallback bool) ([]xgc.DeployResult, []error) {
	return m.deployResults, m.deployErrs
}

func (m *mockExecClient) DestroyAsync(instanceIDs []string) ([]string, []error) {
	return m.destroyedIDs, m.destroyErrs
}

func (m *mockExecClient) WaitForRunning(instanceIDs []string, pollInterval, timeout time.Duration) []string {
	if m.runningIDs != nil {
		return m.runningIDs
	}
	return instanceIDs
}

// --- parseTime ---

func TestParseTime(t *testing.T) {
	tests := []struct {
		input      string
		wantHour   int
		wantMinute int
	}{
		{"08:00", 8, 0},
		{"23:30", 23, 30},
		{"0:00", 0, 0},
		{"8", 8, 0},
		{"12:05", 12, 5},
	}
	for _, tt := range tests {
		h, m := parseTime(tt.input)
		if h != tt.wantHour || m != tt.wantMinute {
			t.Errorf("parseTime(%q) = (%d, %d), want (%d, %d)", tt.input, h, m, tt.wantHour, tt.wantMinute)
		}
	}
}

// --- findMatchingRule ---

func intPtr(n int) *int { return &n }

func TestFindMatchingRule(t *testing.T) {
	tz, _ := time.LoadLocation("Asia/Shanghai")

	twoRules := config.ConfigItem{
		Schedules: []config.ScheduleRule{
			{Time: "08:00", MinCount: intPtr(2)},
			{Time: "20:00", MaxCount: intPtr(0)},
		},
	}

	threeRules := config.ConfigItem{
		Schedules: []config.ScheduleRule{
			{Time: "08:00", MinCount: intPtr(3)},
			{Time: "12:00", MinCount: intPtr(5)},
			{Time: "20:00", MaxCount: intPtr(0)},
		},
	}

	tests := []struct {
		name     string
		cfg      config.ConfigItem
		hour     int
		minute   int
		wantTime string
		wantNil  bool
	}{
		{"during day rule", twoRules, 10, 0, "08:00", false},
		{"during night rule", twoRules, 22, 0, "20:00", false},
		{"wrap around midnight", twoRules, 5, 0, "20:00", false},
		{"exact boundary start", twoRules, 8, 0, "08:00", false},
		{"just before boundary", twoRules, 7, 59, "20:00", false},
		{"exact boundary end", twoRules, 20, 0, "20:00", false},
		{"no rules", config.ConfigItem{}, 10, 0, "", true},
		{
			"single rule always matches",
			config.ConfigItem{Schedules: []config.ScheduleRule{{Time: "12:00", MinCount: intPtr(1)}}},
			3, 0, "12:00", false,
		},
		{"three rules first segment", threeRules, 9, 30, "08:00", false},
		{"three rules middle segment", threeRules, 14, 0, "12:00", false},
		{"three rules last segment", threeRules, 23, 0, "20:00", false},
		{"three rules wrap to last", threeRules, 3, 0, "20:00", false},
		{"three rules boundary 08-12", threeRules, 12, 0, "12:00", false},
		{"three rules boundary 12-20", threeRules, 20, 0, "20:00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2025, 6, 15, tt.hour, tt.minute, 0, 0, tz)
			rule := findMatchingRule(tt.cfg, now, tz)
			if tt.wantNil {
				if rule != nil {
					t.Errorf("expected nil, got rule with time %q", rule.Time)
				}
				return
			}
			if rule == nil {
				t.Fatal("expected a rule, got nil")
			}
			if rule.Time != tt.wantTime {
				t.Errorf("matched rule time = %q, want %q", rule.Time, tt.wantTime)
			}
		})
	}
}

// --- belongsToConfig ---

func TestBelongsToConfig(t *testing.T) {
	imageID := "img-abc12345-xxxx"
	tests := []struct {
		name string
		inst map[string]any
		want bool
	}{
		{"match by image_id", map[string]any{"image_id": imageID}, true},
		{"no match", map[string]any{"image_id": "other"}, false},
		{"empty instance", map[string]any{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := belongsToConfig(tt.inst, imageID); got != tt.want {
				t.Errorf("belongsToConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- groupInstances ---

func TestGroupInstances(t *testing.T) {
	imageID := "test-image"
	preferredModel := "NVIDIA GeForce RTX 4090"

	instances := []map[string]any{
		{"image_id": imageID, "status": "running", "gpu_model": "RTX 4090", "id": "i1"},
		{"image_id": imageID, "status": "running", "gpu_model": "RTX 4090 D", "id": "i2"},
		{"image_id": imageID, "status": "running", "gpu_model": "RTX 4090", "id": "i3"},
		{"image_id": imageID, "status": "stopped", "gpu_model": "RTX 4090", "id": "i4"},
		{"image_id": "other-image", "status": "running", "gpu_model": "RTX 4090", "id": "i5"},
	}

	group := groupInstances(instances, imageID, preferredModel)

	if len(group.preferred) != 2 {
		t.Errorf("preferred count = %d, want 2", len(group.preferred))
	}
	if len(group.fallback) != 1 {
		t.Errorf("fallback count = %d, want 1", len(group.fallback))
	}
	if group.detail["RTX 4090 D"] != 1 {
		t.Errorf("fallback detail[RTX 4090 D] = %d, want 1", group.detail["RTX 4090 D"])
	}
}

func TestGroupInstancesFiltersStatuses(t *testing.T) {
	imageID := "test-image"

	instances := []map[string]any{
		{"image_id": imageID, "status": "deploying", "gpu_model": "RTX 4090", "id": "i1"},
		{"image_id": imageID, "status": "running", "gpu_model": "RTX 4090", "id": "i2"},
		{"image_id": imageID, "status": "booting", "gpu_model": "RTX 4090", "id": "i3"},
		{"image_id": imageID, "status": "shutting_down", "gpu_model": "RTX 4090", "id": "i4"},
		{"image_id": imageID, "status": "shutdown", "gpu_model": "RTX 4090", "id": "i5"},
		{"image_id": imageID, "status": "stopped", "gpu_model": "RTX 4090", "id": "i6"},
		{"image_id": imageID, "status": "error", "gpu_model": "RTX 4090", "id": "i7"},
	}

	group := groupInstances(instances, imageID, "NVIDIA GeForce RTX 4090")

	// deploying, running, booting, shutting_down, shutdown are counted; stopped and error are not
	if len(group.preferred) != 5 {
		t.Errorf("preferred count = %d, want 5 (deploying+running+booting+shutting_down+shutdown)", len(group.preferred))
	}
}

func TestGroupInstancesFallbackSortOrder(t *testing.T) {
	imageID := "test-image"
	preferredModel := "NVIDIA GeForce RTX 4090"

	instances := []map[string]any{
		{"image_id": imageID, "status": "running", "gpu_model": "RTX 4090 D", "id": "d1", "create_timestamp": float64(300)},
		{"image_id": imageID, "status": "running", "gpu_model": "RTX 4090 D 48G", "id": "d48-1", "create_timestamp": float64(200)},
		{"image_id": imageID, "status": "running", "gpu_model": "RTX 4090 D", "id": "d2", "create_timestamp": float64(100)},
		{"image_id": imageID, "status": "running", "gpu_model": "RTX 4090 48G", "id": "n48-1", "create_timestamp": float64(150)},
	}

	group := groupInstances(instances, imageID, preferredModel)

	if len(group.fallback) != 4 {
		t.Fatalf("fallback count = %d, want 4", len(group.fallback))
	}

	// Fallback sorted by reverse priority (least preferred first), then by timestamp within same model.
	// GPUModelsToTry for RTX 4090: [RTX 4090, RTX 4090 D, RTX 4090 D 48G, RTX 4090 48G]
	// Reverse priority order: RTX 4090 48G (idx 3) > RTX 4090 D 48G (idx 2) > RTX 4090 D (idx 1)
	wantOrder := []string{"n48-1", "d48-1", "d2", "d1"}
	for i, wantID := range wantOrder {
		gotID, _ := group.fallback[i]["id"].(string)
		if gotID != wantID {
			t.Errorf("fallback[%d].id = %q, want %q", i, gotID, wantID)
		}
	}
}

// --- pickDestroyTargets ---

func TestPickDestroyTargets(t *testing.T) {
	preferredModel := "NVIDIA GeForce RTX 4090"

	group := instanceGroup{
		preferred: []map[string]any{
			{"id": "p1", "gpu_model": "RTX 4090", "create_timestamp": float64(100)},
			{"id": "p2", "gpu_model": "RTX 4090", "create_timestamp": float64(200)},
		},
		fallback: []map[string]any{
			{"id": "f1", "gpu_model": "RTX 4090 D", "create_timestamp": float64(150)},
		},
	}

	targets := pickDestroyTargets(group, 2, preferredModel)
	if len(targets) != 2 {
		t.Fatalf("targets count = %d, want 2", len(targets))
	}
	// Fallback instance should be destroyed first
	if targets[0].ID != "f1" {
		t.Errorf("first destroy target = %q, want %q (fallback first)", targets[0].ID, "f1")
	}
}

func TestPickDestroyTargetsLimitedByN(t *testing.T) {
	group := instanceGroup{
		preferred: []map[string]any{
			{"id": "p1", "gpu_model": "RTX 4090", "create_timestamp": float64(100)},
			{"id": "p2", "gpu_model": "RTX 4090", "create_timestamp": float64(200)},
		},
		fallback: []map[string]any{
			{"id": "f1", "gpu_model": "RTX 4090 D", "create_timestamp": float64(50)},
			{"id": "f2", "gpu_model": "RTX 4090 D", "create_timestamp": float64(60)},
		},
	}
	targets := pickDestroyTargets(group, 1, "NVIDIA GeForce RTX 4090")
	if len(targets) != 1 {
		t.Fatalf("targets count = %d, want 1", len(targets))
	}
}

// --- truncateConfigKey ---

func TestTruncateConfigKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"abcdefghij", "abcdefgh"},
		{"short", "short"},
		{"12345678", "12345678"},
		{"123456789", "12345678"},
	}
	for _, tt := range tests {
		if got := truncateConfigKey(tt.input); got != tt.want {
			t.Errorf("truncateConfigKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- errCounts ---

func TestErrCounts(t *testing.T) {
	errs := []error{
		errors.New("err A"),
		errors.New("err B"),
		errors.New("err A"),
		errors.New("err A"),
		errors.New("err B"),
	}
	result := errCounts(errs)
	if len(result) != 2 {
		t.Fatalf("errCounts() returned %d entries, want 2", len(result))
	}
	if result[0].Message != "err A" || result[0].Count != 3 {
		t.Errorf("result[0] = {%q, %d}, want {%q, 3}", result[0].Message, result[0].Count, "err A")
	}
	if result[1].Message != "err B" || result[1].Count != 2 {
		t.Errorf("result[1] = {%q, %d}, want {%q, 2}", result[1].Message, result[1].Count, "err B")
	}
}

func TestErrCountsEmpty(t *testing.T) {
	result := errCounts(nil)
	if len(result) != 0 {
		t.Errorf("errCounts(nil) returned %d entries, want 0", len(result))
	}
}

// --- getTimestamp ---

func TestGetTimestamp(t *testing.T) {
	tests := []struct {
		name string
		inst map[string]any
		want int64
	}{
		{"float64", map[string]any{"create_timestamp": float64(1234567890)}, 1234567890},
		{"int", map[string]any{"create_timestamp": int(42)}, 42},
		{"int64", map[string]any{"create_timestamp": int64(99)}, 99},
		{"missing", map[string]any{}, 0},
		{"string", map[string]any{"create_timestamp": "invalid"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getTimestamp(tt.inst); got != tt.want {
				t.Errorf("getTimestamp() = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- instanceRefs ---

func TestInstanceRefs(t *testing.T) {
	instances := []map[string]any{
		{"id": "a1", "gpu_model": "RTX 4090"},
		{"id": "a2", "gpu_model": "RTX 4090 D"},
		{"id": "", "gpu_model": "RTX 4090"},
	}
	refs := instanceRefs(instances)
	if len(refs) != 2 {
		t.Fatalf("instanceRefs() returned %d refs, want 2 (skip empty id)", len(refs))
	}
	if refs[0].ID != "a1" || refs[1].ID != "a2" {
		t.Errorf("refs = %v, want [{a1, RTX 4090}, {a2, RTX 4090 D}]", refs)
	}
}

// --- refIDs / refModelMap ---

func TestRefIDsAndModelMap(t *testing.T) {
	refs := []InstanceRef{
		{ID: "x1", GPUModel: "RTX 4090"},
		{ID: "x2", GPUModel: "RTX 4090 D"},
	}

	ids := refIDs(refs)
	if len(ids) != 2 || ids[0] != "x1" || ids[1] != "x2" {
		t.Errorf("refIDs() = %v, want [x1, x2]", ids)
	}

	mm := refModelMap(refs)
	if mm["x1"] != "RTX 4090" || mm["x2"] != "RTX 4090 D" {
		t.Errorf("refModelMap() = %v", mm)
	}
}

// =============================================================================
// Plan() decision branches
// =============================================================================

func makePlanClient(instances []map[string]any) *mockPlanClient {
	return &mockPlanClient{
		instances: instances,
		images:    map[string]string{"img-001": "test-app"},
	}
}

func TestPlanCreateWhenBelowMin(t *testing.T) {
	client := makePlanClient([]map[string]any{
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i1"},
	})
	configs := []config.ConfigItem{{
		ImageID:  "img-001",
		GPUModel: "NVIDIA GeForce RTX 4090",
		Schedules: []config.ScheduleRule{
			{Time: "00:00", MinCount: intPtr(3)},
		},
	}}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	plans, err := Plan(client, configs, "Asia/Shanghai", time.Date(2025, 6, 15, 10, 0, 0, 0, tz))
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans count = %d, want 1", len(plans))
	}
	p := plans[0]
	if p.Action != "create" {
		t.Errorf("Action = %q, want %q", p.Action, "create")
	}
	if p.Count != 2 {
		t.Errorf("Count = %d, want 2 (min 3 - current 1)", p.Count)
	}
	if !p.AllowFallback {
		t.Error("AllowFallback should be true for min_count create")
	}
}

func TestPlanReplaceWhenMinMetWithFallback(t *testing.T) {
	client := makePlanClient([]map[string]any{
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i1"},
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i2"},
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090 D", "id": "i3"},
	})
	configs := []config.ConfigItem{{
		ImageID:  "img-001",
		GPUModel: "NVIDIA GeForce RTX 4090",
		Schedules: []config.ScheduleRule{
			{Time: "00:00", MinCount: intPtr(2)},
		},
	}}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	plans, err := Plan(client, configs, "Asia/Shanghai", time.Date(2025, 6, 15, 10, 0, 0, 0, tz))
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans count = %d, want 1", len(plans))
	}
	p := plans[0]
	if p.Action != "replace" {
		t.Errorf("Action = %q, want %q", p.Action, "replace")
	}
	if p.Count != 1 {
		t.Errorf("Count = %d, want 1 (1 fallback instance)", p.Count)
	}
	if p.AllowFallback {
		t.Error("AllowFallback should be false for replace")
	}
	if len(p.DestroyTargets) != 1 || p.DestroyTargets[0].GPUModel != "RTX 4090 D" {
		t.Errorf("DestroyTargets should target fallback instance, got %v", p.DestroyTargets)
	}
}

func TestPlanNoOpWhenMinMetNoFallback(t *testing.T) {
	client := makePlanClient([]map[string]any{
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i1"},
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i2"},
	})
	configs := []config.ConfigItem{{
		ImageID:  "img-001",
		GPUModel: "NVIDIA GeForce RTX 4090",
		Schedules: []config.ScheduleRule{
			{Time: "00:00", MinCount: intPtr(2)},
		},
	}}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	plans, err := Plan(client, configs, "Asia/Shanghai", time.Date(2025, 6, 15, 10, 0, 0, 0, tz))
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans count = %d, want 1", len(plans))
	}
	p := plans[0]
	if p.Action != "create" {
		t.Errorf("Action = %q, want %q (no-op create)", p.Action, "create")
	}
	if p.Count != 0 {
		t.Errorf("Count = %d, want 0 (no-op)", p.Count)
	}
}

func TestPlanDestroyWhenAboveMax(t *testing.T) {
	client := makePlanClient([]map[string]any{
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i1", "create_timestamp": float64(100)},
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i2", "create_timestamp": float64(200)},
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i3", "create_timestamp": float64(300)},
	})
	configs := []config.ConfigItem{{
		ImageID:  "img-001",
		GPUModel: "NVIDIA GeForce RTX 4090",
		Schedules: []config.ScheduleRule{
			{Time: "00:00", MaxCount: intPtr(1)},
		},
	}}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	plans, err := Plan(client, configs, "Asia/Shanghai", time.Date(2025, 6, 15, 10, 0, 0, 0, tz))
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans count = %d, want 1", len(plans))
	}
	p := plans[0]
	if p.Action != "destroy" {
		t.Errorf("Action = %q, want %q", p.Action, "destroy")
	}
	if p.Count != 2 {
		t.Errorf("Count = %d, want 2 (3 current - 1 max)", p.Count)
	}
	if len(p.DestroyTargets) != 2 {
		t.Errorf("DestroyTargets count = %d, want 2", len(p.DestroyTargets))
	}
}

func TestPlanReplaceWhenMaxMetWithFallback(t *testing.T) {
	client := makePlanClient([]map[string]any{
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i1"},
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090 D", "id": "i2"},
	})
	configs := []config.ConfigItem{{
		ImageID:  "img-001",
		GPUModel: "NVIDIA GeForce RTX 4090",
		Schedules: []config.ScheduleRule{
			{Time: "00:00", MaxCount: intPtr(3)},
		},
	}}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	plans, err := Plan(client, configs, "Asia/Shanghai", time.Date(2025, 6, 15, 10, 0, 0, 0, tz))
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans count = %d, want 1", len(plans))
	}
	p := plans[0]
	if p.Action != "replace" {
		t.Errorf("Action = %q, want %q", p.Action, "replace")
	}
	if p.Count != 1 {
		t.Errorf("Count = %d, want 1", p.Count)
	}
}

func TestPlanNoOpWhenMaxMetNoFallback(t *testing.T) {
	client := makePlanClient([]map[string]any{
		{"image_id": "img-001", "status": "running", "gpu_model": "RTX 4090", "id": "i1"},
	})
	configs := []config.ConfigItem{{
		ImageID:  "img-001",
		GPUModel: "NVIDIA GeForce RTX 4090",
		Schedules: []config.ScheduleRule{
			{Time: "00:00", MaxCount: intPtr(3)},
		},
	}}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	plans, err := Plan(client, configs, "Asia/Shanghai", time.Date(2025, 6, 15, 10, 0, 0, 0, tz))
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans count = %d, want 1", len(plans))
	}
	p := plans[0]
	if p.Action != "destroy" {
		t.Errorf("Action = %q, want %q (no-op destroy)", p.Action, "destroy")
	}
	if p.Count != 0 {
		t.Errorf("Count = %d, want 0", p.Count)
	}
}

func TestPlanSkipsNoMatchingRule(t *testing.T) {
	client := makePlanClient([]map[string]any{})
	configs := []config.ConfigItem{{
		ImageID:   "img-001",
		GPUModel:  "NVIDIA GeForce RTX 4090",
		Schedules: []config.ScheduleRule{},
	}}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	plans, err := Plan(client, configs, "Asia/Shanghai", time.Date(2025, 6, 15, 10, 0, 0, 0, tz))
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(plans) != 0 {
		t.Errorf("plans count = %d, want 0 (no matching rule)", len(plans))
	}
}

func TestPlanListInstancesError(t *testing.T) {
	client := &mockPlanClient{listErr: errors.New("api failure")}
	configs := []config.ConfigItem{{
		ImageID:  "img-001",
		GPUModel: "NVIDIA GeForce RTX 4090",
		Schedules: []config.ScheduleRule{
			{Time: "00:00", MinCount: intPtr(1)},
		},
	}}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	_, err := Plan(client, configs, "Asia/Shanghai", time.Date(2025, 6, 15, 10, 0, 0, 0, tz))
	if err == nil {
		t.Fatal("Plan() should return error when ListInstances fails")
	}
}

// =============================================================================
// Execute() tests
// =============================================================================

func TestExecuteCreateSuccess(t *testing.T) {
	client := &mockExecClient{
		deployResults: []xgc.DeployResult{
			{ID: "new-1", GPUModel: "NVIDIA GeForce RTX 4090"},
			{ID: "new-2", GPUModel: "NVIDIA GeForce RTX 4090"},
		},
	}
	plans := []ActionPlan{{
		ConfigKey:  "test",
		ImageID:    "img-001",
		Action:     "create",
		Count:      2,
		Current:    1,
		DeployOpts: xgc.DeployOpts{GPUModel: "NVIDIA GeForce RTX 4090"},
	}}

	results := Execute(client, plans)
	if len(results) != 1 {
		t.Fatalf("results count = %d, want 1", len(results))
	}
	r := results[0]
	if r.Action != "create" {
		t.Errorf("Action = %q, want %q", r.Action, "create")
	}
	if len(r.CreatedInstances) != 2 {
		t.Errorf("CreatedInstances count = %d, want 2", len(r.CreatedInstances))
	}
	if r.AfterCount != 3 {
		t.Errorf("AfterCount = %d, want 3 (1 + 2)", r.AfterCount)
	}
	if !r.Success {
		t.Error("Success should be true")
	}
}

func TestExecuteDestroySuccess(t *testing.T) {
	client := &mockExecClient{
		destroyedIDs: []string{"old-1", "old-2"},
	}
	plans := []ActionPlan{{
		ConfigKey:      "test",
		ImageID:        "img-001",
		Action:         "destroy",
		Count:          2,
		Current:        3,
		DestroyTargets: []InstanceRef{{ID: "old-1", GPUModel: "RTX 4090"}, {ID: "old-2", GPUModel: "RTX 4090"}},
		DeployOpts:     xgc.DeployOpts{GPUModel: "NVIDIA GeForce RTX 4090"},
	}}

	results := Execute(client, plans)
	r := results[0]
	if r.Action != "destroy" {
		t.Errorf("Action = %q, want %q", r.Action, "destroy")
	}
	if len(r.DestroyedInstances) != 2 {
		t.Errorf("DestroyedInstances count = %d, want 2", len(r.DestroyedInstances))
	}
	if r.AfterCount != 1 {
		t.Errorf("AfterCount = %d, want 1 (3 - 2)", r.AfterCount)
	}
}

func TestExecuteReplaceSafetyGuard(t *testing.T) {
	client := &mockExecClient{
		deployResults: []xgc.DeployResult{
			{ID: "new-1", GPUModel: "NVIDIA GeForce RTX 4090"},
		},
		runningIDs:   []string{"new-1"},
		destroyedIDs: []string{"old-1"},
	}

	plans := []ActionPlan{{
		ConfigKey: "test",
		ImageID:   "img-001",
		Action:    "replace",
		Count:     3,
		Current:   3,
		DestroyTargets: []InstanceRef{
			{ID: "old-1", GPUModel: "RTX 4090 D"},
			{ID: "old-2", GPUModel: "RTX 4090 D"},
			{ID: "old-3", GPUModel: "RTX 4090 D"},
		},
		DeployOpts: xgc.DeployOpts{GPUModel: "NVIDIA GeForce RTX 4090"},
	}}

	results := Execute(client, plans)
	r := results[0]
	if r.Action != "replace" {
		t.Errorf("Action = %q, want %q", r.Action, "replace")
	}
	if len(r.CreatedInstances) != 1 {
		t.Errorf("CreatedInstances = %d, want 1 (only 1 deployed successfully)", len(r.CreatedInstances))
	}
	if len(r.DestroyedInstances) != 1 {
		t.Errorf("DestroyedInstances = %d, want 1 (safety guard: only destroy as many as created)", len(r.DestroyedInstances))
	}
	if r.Replaced != 1 {
		t.Errorf("Replaced = %d, want 1", r.Replaced)
	}
}

func TestExecuteReplaceZeroDeployed(t *testing.T) {
	client := &mockExecClient{
		deployResults: []xgc.DeployResult{},
		deployErrs:    []error{errors.New("GPU不足")},
	}

	plans := []ActionPlan{{
		ConfigKey: "test",
		ImageID:   "img-001",
		Action:    "replace",
		Count:     2,
		Current:   2,
		DestroyTargets: []InstanceRef{
			{ID: "old-1", GPUModel: "RTX 4090 D"},
			{ID: "old-2", GPUModel: "RTX 4090 D"},
		},
		DeployOpts: xgc.DeployOpts{GPUModel: "NVIDIA GeForce RTX 4090"},
	}}

	results := Execute(client, plans)
	r := results[0]
	if len(r.DestroyedInstances) != 0 {
		t.Errorf("DestroyedInstances = %d, want 0 (should not destroy when no new instances created)", len(r.DestroyedInstances))
	}
	if r.AfterCount != 2 {
		t.Errorf("AfterCount = %d, want 2 (no change)", r.AfterCount)
	}
}
