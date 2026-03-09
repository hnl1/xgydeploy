package config

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

type ScheduleRule struct {
	Time     string `yaml:"time"`
	MinCount *int   `yaml:"min_count"`
	MaxCount *int   `yaml:"max_count"`
}

type ConfigItem struct {
	ImageID      string         `yaml:"image_id"`
	Schedules    []ScheduleRule  `yaml:"schedules"`
	GPUModel     string         `yaml:"gpu_model"`
	GPUCount     int            `yaml:"gpu_count"`
	DataCenterID int            `yaml:"data_center_id"`
}

type configFile struct {
	Timezone string       `yaml:"timezone"`
	Configs  []ConfigItem `yaml:"configs"`
}

func Load(path string) (string, []ConfigItem, error) {
	if secret := os.Getenv("XGC_CONFIG"); secret != "" {
		log.Printf("[config] 从环境变量 XGC_CONFIG 加载配置")
		var cfg configFile
		if err := yaml.Unmarshal([]byte(secret), &cfg); err != nil {
			return "", nil, fmt.Errorf("解析 XGC_CONFIG 失败: %w", err)
		}
		return applyDefaults(cfg)
	}
	if path == "" {
		path = os.Getenv("XGC_CONFIG_PATH")
	}
	if path == "" {
		path = "configs/config.yaml"
	}
	log.Printf("[config] 从文件加载配置: %s", path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("配置文件不存在: %s", path)
	}
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", nil, fmt.Errorf("解析配置失败: %w", err)
	}
	return applyDefaults(cfg)
}

func applyDefaults(cfg configFile) (string, []ConfigItem, error) {
	timezone := cfg.Timezone
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	for i := range cfg.Configs {
		if cfg.Configs[i].GPUModel == "" {
			cfg.Configs[i].GPUModel = "NVIDIA GeForce RTX 4090"
		}
		if cfg.Configs[i].GPUCount == 0 {
			cfg.Configs[i].GPUCount = 1
		}
		if cfg.Configs[i].DataCenterID == 0 {
			cfg.Configs[i].DataCenterID = 1
		}
	}
	return timezone, cfg.Configs, nil
}
