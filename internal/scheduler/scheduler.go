package scheduler

import (
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hnl1/xgydeploy/internal/config"
	"github.com/hnl1/xgydeploy/internal/xgc"
)

var countedStatuses = map[string]bool{
	"deploying":     true,
	"running":       true,
	"booting":       true,
	"shutting_down": true,
	"shutdown":      true,
}

type ActionResult struct {
	ConfigKey   string
	ImageID     string
	Rule        config.ScheduleRule
	BeforeCount int
	AfterCount  int
	Created     []string
	Destroyed   []string
	Success     bool
	Error       string
}

func Run(client *xgc.Client, configs []config.ConfigItem, timezone string, now time.Time) []ActionResult {
	tz, err := time.LoadLocation(timezone)
	if err != nil {
		tz = time.FixedZone("CST", 8*3600)
	}
	if now.IsZero() {
		now = time.Now().In(tz)
	}

	instances, err := client.ListInstances()
	if err != nil {
		log.Printf("[scheduler] 获取实例列表失败: %v", err)
		return []ActionResult{{Success: false, Error: err.Error()}}
	}
	log.Printf("[scheduler] 获取实例列表完成，共 %d 个", len(instances))

	var results []ActionResult
	for _, cfg := range configs {
		rule := findMatchingRule(cfg, now, tz)
		if rule == nil {
			continue
		}
		mine := filterInstances(instances, cfg.ImageID)
		configKey := truncateConfigKey(cfg.ImageID)
		log.Printf("[scheduler] 配置 %s 匹配规则 %s，当前实例数 %d", configKey, rule.Time, len(mine))

		if rule.MinCount != nil {
			results = append(results, applyMinCount(client, cfg, *rule, mine, configKey, now))
		} else if rule.MaxCount != nil {
			results = append(results, applyMaxCount(client, cfg, *rule, mine, configKey))
		}
	}
	return results
}

func truncateConfigKey(imageID string) string {
	if len(imageID) > 8 {
		return imageID[:8]
	}
	return imageID
}

func applyMinCount(client *xgc.Client, cfg config.ConfigItem, rule config.ScheduleRule, mine []map[string]any, configKey string, now time.Time) ActionResult {
	beforeCount := len(mine)
	toCreate := *rule.MinCount - beforeCount
	if toCreate < 0 {
		toCreate = 0
	}
	log.Printf("[scheduler] 配置 %s 创建实例: 目标 %d，当前 %d，需创建 %d", configKey, *rule.MinCount, beforeCount, toCreate)

	created, errs := client.DeployAsync(xgc.DeployOpts{
		Image:        cfg.ImageID,
		GPUModel:     cfg.GPUModel,
		GPUCount:     cfg.GPUCount,
		DataCenterID: cfg.DataCenterID,
		Name:         instanceNamePrefix(cfg.ImageID) + "-" + now.Format("200601021504"),
	}, toCreate)

	return ActionResult{
		ConfigKey:   configKey,
		ImageID:     cfg.ImageID,
		Rule:        rule,
		BeforeCount: beforeCount,
		AfterCount:  beforeCount + len(created),
		Created:     created,
		Success:     len(errs) == 0,
		Error:       firstErr(errs),
	}
}

func applyMaxCount(client *xgc.Client, cfg config.ConfigItem, rule config.ScheduleRule, mine []map[string]any, configKey string) ActionResult {
	beforeCount := len(mine)
	toDestroyCount := beforeCount - *rule.MaxCount
	if toDestroyCount < 0 {
		toDestroyCount = 0
	}
	log.Printf("[scheduler] 配置 %s 销毁实例: 目标 %d，当前 %d，需销毁 %d", configKey, *rule.MaxCount, beforeCount, toDestroyCount)

	toDestroy := pickOldestInstanceIDs(mine, toDestroyCount)
	destroyed, errs := client.DestroyAsync(toDestroy)

	return ActionResult{
		ConfigKey:   configKey,
		ImageID:     cfg.ImageID,
		Rule:        rule,
		BeforeCount: beforeCount,
		AfterCount:  beforeCount - len(destroyed),
		Destroyed:   destroyed,
		Success:     len(errs) == 0,
		Error:       firstErr(errs),
	}
}

func pickOldestInstanceIDs(instances []map[string]any, n int) []string {
	sorted := make([]map[string]any, len(instances))
	copy(sorted, instances)
	sort.Slice(sorted, func(i, j int) bool {
		return getTimestamp(sorted[i]) < getTimestamp(sorted[j])
	})
	var ids []string
	for i := 0; i < n && i < len(sorted); i++ {
		if id, ok := sorted[i]["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func firstErr(errs []error) string {
	if len(errs) > 0 {
		return errs[0].Error()
	}
	return ""
}

func instanceNamePrefix(imageID string) string {
	if len(imageID) < 8 {
		return "xgydeploy-" + imageID
	}
	return "xgydeploy-" + imageID[:8]
}

func belongsToConfig(inst map[string]any, imageID string) bool {
	if img, ok := inst["image"].(string); ok && img == imageID {
		return true
	}
	if img, ok := inst["image_id"].(string); ok && img == imageID {
		return true
	}
	name, _ := inst["name"].(string)
	return strings.HasPrefix(name, instanceNamePrefix(imageID))
}

func filterInstances(instances []map[string]any, imageID string) []map[string]any {
	var result []map[string]any
	for _, i := range instances {
		status, _ := i["status"].(string)
		if belongsToConfig(i, imageID) && countedStatuses[status] {
			result = append(result, i)
		}
	}
	return result
}

func getTimestamp(inst map[string]any) int64 {
	switch v := inst["create_timestamp"].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

func findMatchingRule(cfg config.ConfigItem, now time.Time, tz *time.Location) *config.ScheduleRule {
	local := now.In(tz)
	ch, cm := local.Hour(), local.Minute()
	for _, r := range cfg.Schedules {
		rh, rm := parseTime(r.Time)
		if rh == ch && rm == cm {
			return &r
		}
	}
	return nil
}

func parseTime(s string) (hour, minute int) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	hour, _ = strconv.Atoi(parts[0])
	if len(parts) > 1 {
		minute, _ = strconv.Atoi(parts[1])
	}
	return
}
