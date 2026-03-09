package scheduler

import (
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
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

type ActionPlan struct {
	ConfigKey  string
	ImageID    string
	ImageName  string
	Rule       config.ScheduleRule
	Current    int
	Action     string // "create" | "destroy"
	Count      int
	DestroyIDs []string
	DeployOpts xgc.DeployOpts
}

type ActionResult struct {
	ConfigKey   string
	ImageID     string
	ImageName   string
	Rule        config.ScheduleRule
	BeforeCount int
	AfterCount  int
	Created     []string
	Destroyed   []string
	Success     bool
	Error       string
}

func Plan(client *xgc.Client, configs []config.ConfigItem, timezone string, now time.Time) ([]ActionPlan, error) {
	tz, err := time.LoadLocation(timezone)
	if err != nil {
		tz = time.FixedZone("CST", 8*3600)
	}
	if now.IsZero() {
		now = time.Now().In(tz)
	}

	instances, err := client.ListInstances()
	if err != nil {
		return nil, err
	}
	log.Printf("[scheduler] 获取实例列表完成，共 %d 个", len(instances))

	imageNames, err := client.ListImages()
	if err != nil {
		log.Printf("[scheduler] 获取镜像列表失败，将使用镜像 ID: %v", err)
		imageNames = map[string]string{}
	}

	var plans []ActionPlan
	for _, cfg := range configs {
		rule := findMatchingRule(cfg, now, tz)
		if rule == nil {
			continue
		}
		mine := filterInstances(instances, cfg.ImageID)
		imageName := imageNames[cfg.ImageID]
		configKey := imageName
		if configKey == "" {
			configKey = truncateConfigKey(cfg.ImageID)
		}
		log.Printf("[scheduler] 配置 %s 匹配规则 %s，当前实例数 %d", configKey, rule.Time, len(mine))

		plan := ActionPlan{
			ConfigKey: configKey,
			ImageID:   cfg.ImageID,
			ImageName: imageName,
			Rule:      *rule,
			Current:   len(mine),
		}

		if rule.MinCount != nil {
			toCreate := *rule.MinCount - len(mine)
			if toCreate < 0 {
				toCreate = 0
			}
			plan.Action = "create"
			plan.Count = toCreate
			plan.DeployOpts = xgc.DeployOpts{
				Image:        cfg.ImageID,
				ImageType:    cfg.ImageType,
				GPUModel:     cfg.GPUModel,
				GPUCount:     cfg.GPUCount,
				DataCenterID: cfg.DataCenterID,
			}
		} else if rule.MaxCount != nil {
			toDestroy := len(mine) - *rule.MaxCount
			if toDestroy < 0 {
				toDestroy = 0
			}
			plan.Action = "destroy"
			plan.Count = toDestroy
			plan.DestroyIDs = pickOldestInstanceIDs(mine, toDestroy)
		}

		plans = append(plans, plan)
	}
	return plans, nil
}

func Execute(client *xgc.Client, plans []ActionPlan) []ActionResult {
	results := make([]ActionResult, len(plans))
	var wg sync.WaitGroup

	for i, plan := range plans {
		i, plan := i, plan
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch plan.Action {
			case "create":
				results[i] = executeCreate(client, plan)
			case "destroy":
				results[i] = executeDestroy(client, plan)
			default:
				results[i] = ActionResult{
					ConfigKey:   plan.ConfigKey,
					ImageID:     plan.ImageID,
					ImageName:   plan.ImageName,
					Rule:        plan.Rule,
					BeforeCount: plan.Current,
					AfterCount:  plan.Current,
					Success:     true,
				}
			}
		}()
	}
	wg.Wait()
	return results
}

func executeCreate(client *xgc.Client, plan ActionPlan) ActionResult {
	log.Printf("[scheduler] 配置 %s 开始创建 %d 个实例", plan.ConfigKey, plan.Count)

	created, errs := client.DeployAsync(plan.DeployOpts, plan.Count)

	verified := created
	if len(created) > 0 {
		log.Printf("[scheduler] 配置 %s 等待 %d 个实例启动完成...", plan.ConfigKey, len(created))
		verified = client.WaitForRunning(created, 15*time.Second, 5*time.Minute)
		log.Printf("[scheduler] 配置 %s 已 running: %d/%d", plan.ConfigKey, len(verified), len(created))
	}

	return ActionResult{
		ConfigKey:   plan.ConfigKey,
		ImageID:     plan.ImageID,
		ImageName:   plan.ImageName,
		Rule:        plan.Rule,
		BeforeCount: plan.Current,
		AfterCount:  plan.Current + len(verified),
		Created:     verified,
		Success:     len(errs) == 0 && len(verified) == len(created),
		Error:       firstErr(errs),
	}
}

func executeDestroy(client *xgc.Client, plan ActionPlan) ActionResult {
	log.Printf("[scheduler] 配置 %s 开始销毁 %d 个实例", plan.ConfigKey, plan.Count)

	destroyed, errs := client.DestroyAsync(plan.DestroyIDs)

	return ActionResult{
		ConfigKey:   plan.ConfigKey,
		ImageID:     plan.ImageID,
		ImageName:   plan.ImageName,
		Rule:        plan.Rule,
		BeforeCount: plan.Current,
		AfterCount:  plan.Current - len(destroyed),
		Destroyed:   destroyed,
		Success:     len(errs) == 0,
		Error:       firstErr(errs),
	}
}

func truncateConfigKey(imageID string) string {
	if len(imageID) > 8 {
		return imageID[:8]
	}
	return imageID
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

const minutesPerDay = 24 * 60

func findMatchingRule(cfg config.ConfigItem, now time.Time, tz *time.Location) *config.ScheduleRule {
	local := now.In(tz)
	nowMinutes := local.Hour()*60 + local.Minute()

	type ruleAt struct {
		min int
		r   config.ScheduleRule
	}
	var rules []ruleAt
	for _, r := range cfg.Schedules {
		rh, rm := parseTime(r.Time)
		rules = append(rules, ruleAt{rh*60 + rm, r})
	}
	if len(rules) == 0 {
		return nil
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].min < rules[j].min })

	for i := range rules {
		start := rules[i].min
		if i+1 < len(rules) {
			end := rules[i+1].min
			if nowMinutes >= start && nowMinutes < end {
				return &rules[i].r
			}
		} else {
			if nowMinutes >= start || nowMinutes < rules[0].min {
				return &rules[i].r
			}
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
