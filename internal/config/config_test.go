package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyDefaults(t *testing.T) {
	cfg := configFile{
		Configs: []ConfigItem{
			{ImageID: "img-001"},
		},
	}

	tz, items, err := applyDefaults(cfg)
	if err != nil {
		t.Fatalf("applyDefaults() error: %v", err)
	}
	if tz != "Asia/Shanghai" {
		t.Errorf("timezone = %q, want %q", tz, "Asia/Shanghai")
	}
	if len(items) != 1 {
		t.Fatalf("items count = %d, want 1", len(items))
	}
	item := items[0]
	if item.ImageType != "private" {
		t.Errorf("ImageType = %q, want %q", item.ImageType, "private")
	}
	if item.GPUModel != "NVIDIA GeForce RTX 4090" {
		t.Errorf("GPUModel = %q, want %q", item.GPUModel, "NVIDIA GeForce RTX 4090")
	}
	if item.GPUCount != 1 {
		t.Errorf("GPUCount = %d, want 1", item.GPUCount)
	}
	if item.DataCenterID != 1 {
		t.Errorf("DataCenterID = %d, want 1", item.DataCenterID)
	}
}

func TestApplyDefaultsPreservesExplicitValues(t *testing.T) {
	cfg := configFile{
		Timezone: "UTC",
		Configs: []ConfigItem{
			{
				ImageID:      "img-002",
				ImageType:    "public",
				GPUModel:     "NVIDIA GeForce RTX 4090 D",
				GPUCount:     2,
				DataCenterID: 5,
			},
		},
	}

	tz, items, err := applyDefaults(cfg)
	if err != nil {
		t.Fatalf("applyDefaults() error: %v", err)
	}
	if tz != "UTC" {
		t.Errorf("timezone = %q, want %q", tz, "UTC")
	}
	item := items[0]
	if item.ImageType != "public" {
		t.Errorf("ImageType = %q, want %q", item.ImageType, "public")
	}
	if item.GPUModel != "NVIDIA GeForce RTX 4090 D" {
		t.Errorf("GPUModel = %q, want %q", item.GPUModel, "NVIDIA GeForce RTX 4090 D")
	}
	if item.GPUCount != 2 {
		t.Errorf("GPUCount = %d, want 2", item.GPUCount)
	}
	if item.DataCenterID != 5 {
		t.Errorf("DataCenterID = %d, want 5", item.DataCenterID)
	}
}

func TestLoadFromEnvVar(t *testing.T) {
	yaml := `
timezone: "America/New_York"
configs:
  - image_id: "img-env"
    gpu_count: 3
`
	t.Setenv("XGC_CONFIG", yaml)
	t.Setenv("XGC_CONFIG_PATH", "")

	tz, items, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if tz != "America/New_York" {
		t.Errorf("timezone = %q, want %q", tz, "America/New_York")
	}
	if len(items) != 1 {
		t.Fatalf("items count = %d, want 1", len(items))
	}
	if items[0].ImageID != "img-env" {
		t.Errorf("ImageID = %q, want %q", items[0].ImageID, "img-env")
	}
	if items[0].GPUCount != 3 {
		t.Errorf("GPUCount = %d, want 3", items[0].GPUCount)
	}
}

func TestLoadFromFile(t *testing.T) {
	yaml := `
timezone: "Europe/London"
configs:
  - image_id: "img-file"
    image_type: "public"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test-config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	t.Setenv("XGC_CONFIG", "")
	t.Setenv("XGC_CONFIG_PATH", path)

	tz, items, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if tz != "Europe/London" {
		t.Errorf("timezone = %q, want %q", tz, "Europe/London")
	}
	if len(items) != 1 {
		t.Fatalf("items count = %d, want 1", len(items))
	}
	if items[0].ImageType != "public" {
		t.Errorf("ImageType = %q, want %q", items[0].ImageType, "public")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	t.Setenv("XGC_CONFIG", "{{invalid yaml")
	t.Setenv("XGC_CONFIG_PATH", "")

	_, _, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid YAML, got nil")
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("XGC_CONFIG", "")
	t.Setenv("XGC_CONFIG_PATH", "/nonexistent/path/config.yaml")

	_, _, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for missing file, got nil")
	}
}

func TestRawYAMLFromEnv(t *testing.T) {
	t.Setenv("XGC_CONFIG", "raw-yaml-content")
	raw := RawYAML()
	if raw != "raw-yaml-content" {
		t.Errorf("RawYAML() = %q, want %q", raw, "raw-yaml-content")
	}
}

func TestRawYAMLFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "raw.yaml")
	content := "timezone: UTC\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	t.Setenv("XGC_CONFIG", "")
	t.Setenv("XGC_CONFIG_PATH", path)

	raw := RawYAML()
	if raw != content {
		t.Errorf("RawYAML() = %q, want %q", raw, content)
	}
}

func TestRawYAMLMissingFile(t *testing.T) {
	t.Setenv("XGC_CONFIG", "")
	t.Setenv("XGC_CONFIG_PATH", "/nonexistent/file.yaml")

	raw := RawYAML()
	if raw != "" {
		t.Errorf("RawYAML() = %q, want empty", raw)
	}
}
