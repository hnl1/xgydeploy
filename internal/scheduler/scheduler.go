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

type InstanceRef struct {
	ID       string
	GPUModel string // 短型号名
}

type ActionPlan struct {
	ConfigKey      string
	ImageID        string
	ImageName      string
	Rule           config.ScheduleRule
	Current        int
	PreferredCount int
	FallbackCount  int
	FallbackDetail map[string]int // 短型号名 → 数量
	Action         string         // "create" | "destroy" | "replace"
	AllowFallback  bool
	Count          int
	DestroyTargets []InstanceRef
	DeployOpts     xgc.DeployOpts
}

type ActionResult struct {
	ConfigKey          string
	ImageID            string
	ImageName          string
	Rule               config.ScheduleRule
	Action             string // "create" | "destroy" | "replace"
	BeforeCount        int
	AfterCount         int
	CreatedInstances   []InstanceRef
	DestroyedInstances []InstanceRef
	PlannedGPUModel    string
	Replaced           int
	Success            bool
	Errors             []ErrCount
}

type instanceGroup struct {
	preferred []map[string]any
	fallback  []map[string]any
	detail    map[string]int
}

// groupInstances 将属于指定 config 的实例按 GPU 型号分为首选和回退两组。
// 回退组按回退优先级反序排列（最不优先的在前，方便按序销毁）。
func groupInstances(instances []map[string]any, imageID, preferredModel string) instanceGroup {
	shortPreferred := xgc.GPUModelShortName(preferredModel)
	fallbackModels := xgc.GPUModelsToTry(preferredModel)

	var preferred, fallback []map[string]any
	detail := map[string]int{}

	for _, inst := range instances {
		status, _ := inst["status"].(string)
		if !belongsToConfig(inst, imageID) || !countedStatuses[status] {
			continue
		}
		gpuModel, _ := inst["gpu_model"].(string)
		if gpuModel == shortPreferred {
			preferred = append(preferred, inst)
		} else {
			fallback = append(fallback, inst)
			detail[gpuModel]++
		}
	}

	fallbackOrder := map[string]int{}
	for i, m := range fallbackModels {
		fallbackOrder[xgc.GPUModelShortName(m)] = i
	}
	sort.Slice(fallback, func(i, j int) bool {
		gi, _ := fallback[i]["gpu_model"].(string)
		gj, _ := fallback[j]["gpu_model"].(string)
		oi := fallbackOrder[gi]
		oj := fallbackOrder[gj]
		if oi != oj {
			return oi > oj
		}
		return getTimestamp(fallback[i]) < getTimestamp(fallback[j])
	})

	return instanceGroup{preferred: preferred, fallback: fallback, detail: detail}
}

func Plan(client *xgc.Client, configs []config.ConfigItem, timezone string, now time.Time) ([]ActionPlan, error) {
	tz, err := time.LoadLocation(timezone)
	if err != nil {
		tz = time.UTC
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
	for i, cfg := range configs {
		rule := findMatchingRule(cfg, now, tz)
		if rule == nil {
			continue
		}

		group := groupInstances(instances, cfg.ImageID, cfg.GPUModel)
		total := len(group.preferred) + len(group.fallback)

		imageName := imageNames[cfg.ImageID]
		configKey := imageName
		if configKey == "" {
			configKey = truncateConfigKey(cfg.ImageID)
		}
		log.Printf("[scheduler] 配置 #%d 匹配规则，首选[%s] %d 台，回退 %d 台，共 %d 台",
			i+1, xgc.GPUModelShortName(cfg.GPUModel), len(group.preferred), len(group.fallback), total)

		deployOpts := xgc.DeployOpts{
			Image:        cfg.ImageID,
			ImageType:    cfg.ImageType,
			GPUModel:     cfg.GPUModel,
			GPUCount:     cfg.GPUCount,
			DataCenterID: cfg.DataCenterID,
		}

		basePlan := ActionPlan{
			ConfigKey:      configKey,
			ImageID:        cfg.ImageID,
			ImageName:      imageName,
			Rule:           *rule,
			Current:        total,
			PreferredCount: len(group.preferred),
			FallbackCount:  len(group.fallback),
			FallbackDetail: group.detail,
			DeployOpts:     deployOpts,
		}

		if rule.MinCount != nil {
			if total < *rule.MinCount {
				plan := basePlan
				plan.Action = "create"
				plan.Count = *rule.MinCount - total
				plan.AllowFallback = true
				plans = append(plans, plan)
			} else if len(group.fallback) > 0 {
				plan := basePlan
				plan.Action = "replace"
				plan.Count = len(group.fallback)
				plan.AllowFallback = false
				plan.DestroyTargets = instanceRefs(group.fallback)
				plans = append(plans, plan)
			} else {
				plan := basePlan
				plan.Action = "create"
				plan.Count = 0
				plans = append(plans, plan)
			}
		} else if rule.MaxCount != nil {
			if total > *rule.MaxCount {
				plan := basePlan
				plan.Action = "destroy"
				plan.Count = total - *rule.MaxCount
				plan.DestroyTargets = pickDestroyTargets(group, total-*rule.MaxCount)
				plans = append(plans, plan)
			} else if len(group.fallback) > 0 {
				plan := basePlan
				plan.Action = "replace"
				plan.Count = len(group.fallback)
				plan.AllowFallback = false
				plan.DestroyTargets = instanceRefs(group.fallback)
				plans = append(plans, plan)
			} else {
				plan := basePlan
				plan.Action = "destroy"
				plan.Count = 0
				plans = append(plans, plan)
			}
		}
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
			case "replace":
				results[i] = executeReplace(client, plan)
			default:
				results[i] = ActionResult{
					ConfigKey:       plan.ConfigKey,
					ImageID:         plan.ImageID,
					ImageName:       plan.ImageName,
					Rule:            plan.Rule,
					Action:          plan.Action,
					BeforeCount:     plan.Current,
					AfterCount:      plan.Current,
					PlannedGPUModel: xgc.GPUModelShortName(plan.DeployOpts.GPUModel),
					Success:         true,
				}
			}
		}()
	}
	wg.Wait()
	return results
}

func executeCreate(client *xgc.Client, plan ActionPlan) ActionResult {
	log.Printf("[scheduler] 开始创建 %d 个实例（允许回退=%v）", plan.Count, plan.AllowFallback)

	deployed, errs := client.DeployAsync(plan.DeployOpts, plan.Count, plan.AllowFallback)

	modelMap := map[string]string{}
	var newIDs []string
	for _, r := range deployed {
		newIDs = append(newIDs, r.ID)
		modelMap[r.ID] = xgc.GPUModelShortName(r.GPUModel)
	}

	verified := newIDs
	if len(newIDs) > 0 {
		log.Printf("[scheduler] 等待 %d 个实例启动完成...", len(newIDs))
		verified = client.WaitForRunning(newIDs, 15*time.Second, 5*time.Minute)
		log.Printf("[scheduler] 已启动: %d/%d", len(verified), len(newIDs))
	}

	var created []InstanceRef
	for _, id := range verified {
		created = append(created, InstanceRef{ID: id, GPUModel: modelMap[id]})
	}

	return ActionResult{
		ConfigKey:        plan.ConfigKey,
		ImageID:          plan.ImageID,
		ImageName:        plan.ImageName,
		Rule:             plan.Rule,
		Action:           "create",
		BeforeCount:      plan.Current,
		AfterCount:       plan.Current + len(verified),
		CreatedInstances: created,
		PlannedGPUModel:  xgc.GPUModelShortName(plan.DeployOpts.GPUModel),
		Success:          len(errs) == 0 && len(verified) == len(deployed),
		Errors:           errCounts(errs),
	}
}

func executeDestroy(client *xgc.Client, plan ActionPlan) ActionResult {
	log.Printf("[scheduler] 开始销毁 %d 个实例", plan.Count)

	destroyIDs := refIDs(plan.DestroyTargets)
	destroyed, errs := client.DestroyAsync(destroyIDs)

	destroyModelMap := refModelMap(plan.DestroyTargets)
	var destroyedRefs []InstanceRef
	for _, id := range destroyed {
		destroyedRefs = append(destroyedRefs, InstanceRef{ID: id, GPUModel: destroyModelMap[id]})
	}

	return ActionResult{
		ConfigKey:          plan.ConfigKey,
		ImageID:            plan.ImageID,
		ImageName:          plan.ImageName,
		Rule:               plan.Rule,
		Action:             "destroy",
		BeforeCount:        plan.Current,
		AfterCount:         plan.Current - len(destroyed),
		DestroyedInstances: destroyedRefs,
		PlannedGPUModel:    xgc.GPUModelShortName(plan.DeployOpts.GPUModel),
		Success:            len(errs) == 0,
		Errors:             errCounts(errs),
	}
}

func executeReplace(client *xgc.Client, plan ActionPlan) ActionResult {
	preferred := xgc.GPUModelShortName(plan.DeployOpts.GPUModel)
	log.Printf("[scheduler] 开始替换 %d 个非[%s]实例", plan.Count, preferred)

	deployed, errs := client.DeployAsync(plan.DeployOpts, plan.Count, false)

	modelMap := map[string]string{}
	var newIDs []string
	for _, r := range deployed {
		newIDs = append(newIDs, r.ID)
		modelMap[r.ID] = xgc.GPUModelShortName(r.GPUModel)
	}

	verified := newIDs
	if len(newIDs) > 0 {
		log.Printf("[scheduler] 等待 %d 个替换实例启动完成...", len(newIDs))
		verified = client.WaitForRunning(newIDs, 15*time.Second, 5*time.Minute)
		log.Printf("[scheduler] 替换实例已启动: %d/%d", len(verified), len(newIDs))
	}

	var created []InstanceRef
	for _, id := range verified {
		created = append(created, InstanceRef{ID: id, GPUModel: modelMap[id]})
	}

	toDestroy := plan.DestroyTargets
	if len(toDestroy) > len(verified) {
		toDestroy = toDestroy[:len(verified)]
	}
	var destroyedRefs []InstanceRef
	if len(toDestroy) > 0 {
		destroyIDs := refIDs(toDestroy)
		log.Printf("[scheduler] 销毁 %d 个被替换实例", len(destroyIDs))
		destroyModelMap := refModelMap(toDestroy)
		var destroyErrs []error
		destroyed, destroyErrs := client.DestroyAsync(destroyIDs)
		for _, e := range destroyErrs {
			errs = append(errs, e)
		}
		for _, id := range destroyed {
			destroyedRefs = append(destroyedRefs, InstanceRef{ID: id, GPUModel: destroyModelMap[id]})
		}
	}

	return ActionResult{
		ConfigKey:          plan.ConfigKey,
		ImageID:            plan.ImageID,
		ImageName:          plan.ImageName,
		Rule:               plan.Rule,
		Action:             "replace",
		BeforeCount:        plan.Current,
		AfterCount:         plan.Current + len(created) - len(destroyedRefs),
		CreatedInstances:   created,
		DestroyedInstances: destroyedRefs,
		PlannedGPUModel:    preferred,
		Replaced:           len(destroyedRefs),
		Success:            len(errs) == 0,
		Errors:             errCounts(errs),
	}
}

func instanceRefs(instances []map[string]any) []InstanceRef {
	refs := make([]InstanceRef, 0, len(instances))
	for _, inst := range instances {
		id, _ := inst["id"].(string)
		model, _ := inst["gpu_model"].(string)
		if id != "" {
			refs = append(refs, InstanceRef{ID: id, GPUModel: model})
		}
	}
	return refs
}

func pickDestroyTargets(group instanceGroup, n int) []InstanceRef {
	var targets []InstanceRef
	for _, inst := range group.fallback {
		if len(targets) >= n {
			break
		}
		id, _ := inst["id"].(string)
		model, _ := inst["gpu_model"].(string)
		if id != "" {
			targets = append(targets, InstanceRef{ID: id, GPUModel: model})
		}
	}
	if len(targets) < n {
		targets = append(targets, pickOldestRefs(group.preferred, n-len(targets))...)
	}
	return targets
}

func refIDs(refs []InstanceRef) []string {
	ids := make([]string, len(refs))
	for i, r := range refs {
		ids[i] = r.ID
	}
	return ids
}

func refModelMap(refs []InstanceRef) map[string]string {
	m := make(map[string]string, len(refs))
	for _, r := range refs {
		m[r.ID] = r.GPUModel
	}
	return m
}

func truncateConfigKey(imageID string) string {
	if len(imageID) > 8 {
		return imageID[:8]
	}
	return imageID
}

func pickOldestRefs(instances []map[string]any, n int) []InstanceRef {
	sorted := make([]map[string]any, len(instances))
	copy(sorted, instances)
	sort.Slice(sorted, func(i, j int) bool {
		return getTimestamp(sorted[i]) < getTimestamp(sorted[j])
	})
	var refs []InstanceRef
	for i := 0; i < n && i < len(sorted); i++ {
		id, _ := sorted[i]["id"].(string)
		model, _ := sorted[i]["gpu_model"].(string)
		if id != "" {
			refs = append(refs, InstanceRef{ID: id, GPUModel: model})
		}
	}
	return refs
}

type ErrCount struct {
	Message string
	Count   int
}

func errCounts(errs []error) []ErrCount {
	order := []string{}
	counts := map[string]int{}
	for _, e := range errs {
		msg := e.Error()
		if counts[msg] == 0 {
			order = append(order, msg)
		}
		counts[msg]++
	}
	result := make([]ErrCount, len(order))
	for i, msg := range order {
		result[i] = ErrCount{Message: msg, Count: counts[msg]}
	}
	return result
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
