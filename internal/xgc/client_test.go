package xgc

import (
	"errors"
	"testing"
)

func TestGPUModelShortName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"NVIDIA GeForce RTX 4090", "RTX 4090"},
		{"NVIDIA GeForce RTX 4090 D", "RTX 4090 D"},
		{"NVIDIA GeForce RTX 4090 48G", "RTX 4090 48G"},
		{"NVIDIA GeForce RTX 4090 D 48G", "RTX 4090 D 48G"},
		{"UnknownModel", "UnknownModel"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := GPUModelShortName(tt.input); got != tt.want {
			t.Errorf("GPUModelShortName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGPUModelsToTry(t *testing.T) {
	tests := []struct {
		primary string
		want    []string
	}{
		{
			"NVIDIA GeForce RTX 4090",
			[]string{
				"NVIDIA GeForce RTX 4090",
				"NVIDIA GeForce RTX 4090 D",
				"NVIDIA GeForce RTX 4090 D 48G",
				"NVIDIA GeForce RTX 4090 48G",
			},
		},
		{
			"NVIDIA GeForce RTX 4090 D",
			[]string{
				"NVIDIA GeForce RTX 4090 D",
				"NVIDIA GeForce RTX 4090",
				"NVIDIA GeForce RTX 4090 D 48G",
				"NVIDIA GeForce RTX 4090 48G",
			},
		},
		{
			"NVIDIA GeForce RTX 4090 48G",
			[]string{
				"NVIDIA GeForce RTX 4090 48G",
				"NVIDIA GeForce RTX 4090 D 48G",
			},
		},
		{
			"NVIDIA GeForce RTX 4090 D 48G",
			[]string{
				"NVIDIA GeForce RTX 4090 D 48G",
				"NVIDIA GeForce RTX 4090 48G",
			},
		},
		{
			"UnknownGPU",
			[]string{"UnknownGPU"},
		},
	}
	for _, tt := range tests {
		got := GPUModelsToTry(tt.primary)
		if len(got) != len(tt.want) {
			t.Errorf("GPUModelsToTry(%q): got %d models, want %d\n  got:  %v\n  want: %v",
				tt.primary, len(got), len(tt.want), got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("GPUModelsToTry(%q)[%d] = %q, want %q", tt.primary, i, got[i], tt.want[i])
			}
		}
	}
}

func TestIsGPUUnavailable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{&APIError{Code: 1, Msg: "可用GPU不足"}, true},
		{&APIError{Code: 2, Msg: "GPU型号暂时不可用"}, true},
		{&APIError{Code: 3, Msg: "余额不足"}, false},
		{errors.New("network error"), false},
		{nil, false},
	}
	for _, tt := range tests {
		if tt.err == nil {
			continue
		}
		if got := isGPUUnavailable(tt.err); got != tt.want {
			t.Errorf("isGPUUnavailable(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		input any
		want  int
	}{
		{float64(42), 42},
		{int(7), 7},
		{int64(100), 100},
		{"hello", 0},
		{nil, 0},
	}
	for _, tt := range tests {
		if got := toInt(tt.input); got != tt.want {
			t.Errorf("toInt(%v) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestAPIErrorMessage(t *testing.T) {
	e := &APIError{Code: 500, Msg: "服务器错误"}
	if e.Error() != "服务器错误" {
		t.Errorf("APIError.Error() = %q, want %q", e.Error(), "服务器错误")
	}
}

// =============================================================================
// deployWithRetry tests
// =============================================================================

var noDelay = retryConfig{retriesPerModel: 3, delayFn: nil}

func TestDeployWithRetryPrimarySuccess(t *testing.T) {
	calls := 0
	deployFn := func(opts DeployOpts) (string, error) {
		calls++
		return "inst-1", nil
	}

	result, err := deployWithRetry(deployFn, DeployOpts{GPUModel: "A"}, []string{"A"}, noDelay)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "inst-1" || result.GPUModel != "A" {
		t.Errorf("result = %+v, want {inst-1, A}", result)
	}
	if calls != 1 {
		t.Errorf("deploy called %d times, want 1", calls)
	}
}

func TestDeployWithRetryRetryThenSuccess(t *testing.T) {
	calls := 0
	deployFn := func(opts DeployOpts) (string, error) {
		calls++
		if calls < 3 {
			return "", &APIError{Msg: "可用GPU不足"}
		}
		return "inst-1", nil
	}

	result, err := deployWithRetry(deployFn, DeployOpts{GPUModel: "A"}, []string{"A"}, noDelay)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "inst-1" {
		t.Errorf("result.ID = %q, want %q", result.ID, "inst-1")
	}
	if calls != 3 {
		t.Errorf("deploy called %d times, want 3", calls)
	}
}

func TestDeployWithRetryFallbackToSecondModel(t *testing.T) {
	calls := 0
	deployFn := func(opts DeployOpts) (string, error) {
		calls++
		if opts.GPUModel == "A" {
			return "", &APIError{Msg: "可用GPU不足"}
		}
		return "inst-fallback", nil
	}

	result, err := deployWithRetry(deployFn, DeployOpts{GPUModel: "A"}, []string{"A", "B"}, noDelay)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GPUModel != "B" {
		t.Errorf("result.GPUModel = %q, want %q (fallback)", result.GPUModel, "B")
	}
	// 3 retries on A + 1 success on B = 4
	if calls != 4 {
		t.Errorf("deploy called %d times, want 4 (3 retries on A + 1 on B)", calls)
	}
}

func TestDeployWithRetryNonGPUErrorStopsImmediately(t *testing.T) {
	calls := 0
	deployFn := func(opts DeployOpts) (string, error) {
		calls++
		return "", &APIError{Msg: "余额不足"}
	}

	_, err := deployWithRetry(deployFn, DeployOpts{GPUModel: "A"}, []string{"A", "B", "C"}, noDelay)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("deploy called %d times, want 1 (non-GPU error should stop immediately)", calls)
	}
}

func TestDeployWithRetryAllModelsExhausted(t *testing.T) {
	calls := 0
	deployFn := func(opts DeployOpts) (string, error) {
		calls++
		return "", &APIError{Msg: "可用GPU不足"}
	}

	_, err := deployWithRetry(deployFn, DeployOpts{GPUModel: "A"}, []string{"A", "B"}, noDelay)
	if err == nil {
		t.Fatal("expected error when all models exhausted")
	}
	// 3 retries on A + 3 retries on B = 6
	if calls != 6 {
		t.Errorf("deploy called %d times, want 6 (3 retries × 2 models)", calls)
	}
}

func TestDeployWithRetryNonAPIError(t *testing.T) {
	calls := 0
	deployFn := func(opts DeployOpts) (string, error) {
		calls++
		return "", errors.New("network timeout")
	}

	_, err := deployWithRetry(deployFn, DeployOpts{GPUModel: "A"}, []string{"A", "B"}, noDelay)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("deploy called %d times, want 1 (non-API error should stop)", calls)
	}
}
