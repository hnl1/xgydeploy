package xgc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://api.xiangongyun.com"

type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	return e.Msg
}

type Client struct {
	token  string
	client *http.Client
}

func New() (*Client, error) {
	token := os.Getenv("XGC_API_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("需要设置 XGC_API_TOKEN 环境变量")
	}
	return &Client{
		token:  token,
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

const networkRetryAttempts = 3

func (c *Client) request(method, path string, body any) (map[string]any, error) {
	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	var lastErr error
	for attempt := 1; attempt <= networkRetryAttempts; attempt++ {
		req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", c.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < networkRetryAttempts {
				log.Printf("[xgc] 请求 %s %s 网络错误 (%d/%d): %v，重试中...", method, path, attempt, networkRetryAttempts, err)
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
				continue
			}
			return nil, lastErr
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode >= 400 {
			log.Printf("[xgc] API 请求失败: %s %s -> %s", method, path, resp.Status)
			return nil, fmt.Errorf("API 错误: %s", resp.Status)
		}
		if resp.ContentLength == 0 {
			return nil, nil
		}
		var data map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, lastErr
}

func (c *Client) ListInstances() ([]map[string]any, error) {
	data, err := c.request("GET", "/open/instances", nil)
	if err != nil {
		return nil, err
	}
	if data == nil {
		log.Printf("[xgc] ListInstances 响应为空")
		return []map[string]any{}, nil
	}
	inner, _ := data["data"].(map[string]any)
	if inner == nil {
		inner = data
	}
	list, ok := inner["list"].([]any)
	if !ok {
		keys := make([]string, 0, len(data))
		for k := range data {
			keys = append(keys, k)
		}
		log.Printf("[xgc] ListInstances 响应无 list 字段，顶层 keys=%v", keys)
		return []map[string]any{}, nil
	}
	result := make([]map[string]any, 0, len(list))
	for _, v := range list {
		if m, ok := v.(map[string]any); ok {
			result = append(result, m)
		}
	}
	return result, nil
}

func (c *Client) GetBalance() (float64, error) {
	data, err := c.request("GET", "/open/balance", nil)
	if err != nil {
		return 0, err
	}
	if data == nil {
		return 0, nil
	}
	inner, _ := data["data"].(map[string]any)
	if inner == nil {
		inner = data
	}
	balance, _ := inner["balance"].(float64)
	return balance, nil
}

// ListImages 返回私有镜像列表，用于 image_id → name 映射。
func (c *Client) ListImages() (map[string]string, error) {
	data, err := c.request("GET", "/open/images", nil)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return map[string]string{}, nil
	}
	inner, _ := data["data"].(map[string]any)
	if inner == nil {
		inner = data
	}
	list, _ := inner["list"].([]any)
	result := make(map[string]string, len(list))
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		name, _ := m["name"].(string)
		if id != "" && name != "" {
			result[id] = name
		}
	}
	return result, nil
}

type DeployOpts struct {
	Image        string
	ImageType    string // public | private
	GPUModel     string
	GPUCount     int
	DataCenterID int
}

func (c *Client) Deploy(opts DeployOpts) (string, error) {
	body := map[string]any{
		"image":                   opts.Image,
		"gpu_model":               opts.GPUModel,
		"gpu_count":               opts.GPUCount,
		"data_center_id":          opts.DataCenterID,
		"image_type":              opts.ImageType,
		"storage":                 false,
		"storage_mount_path":      "/root/cloud",
		"system_disk_expand":      false,
		"system_disk_expand_size": 0,
	}
	data, err := c.request("POST", "/open/instance/deploy", body)
	if err != nil {
		return "", err
	}
	inner, _ := data["data"].(map[string]any)
	if inner == nil {
		inner = data
	}
	id, _ := inner["id"].(string)
	if id == "" && data != nil {
		code := toInt(data["code"])
		msg, _ := data["msg"].(string)
		if msg != "" {
			log.Printf("[xgc] deploy 失败: code=%d msg=%q", code, msg)
			return "", &APIError{Code: code, Msg: msg}
		}
		log.Printf("[xgc] deploy 失败: code=%d 响应缺少 id", code)
		return "", &APIError{Code: code, Msg: "deploy 响应缺少 id"}
	}
	return id, nil
}

func (c *Client) Destroy(instanceID string) error {
	_, err := c.request("POST", "/open/instance/destroy", map[string]any{"id": instanceID})
	return err
}

// WaitForRunning 轮询直到指定实例全部 status=running，或超时。返回已 running 的 ID 列表。
func (c *Client) WaitForRunning(instanceIDs []string, pollInterval, timeout time.Duration) []string {
	if len(instanceIDs) == 0 {
		return nil
	}
	idSet := make(map[string]bool)
	for _, id := range instanceIDs {
		idSet[id] = true
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		all, err := c.ListInstances()
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}
		running := make(map[string]bool)
		for _, inst := range all {
			id, _ := inst["id"].(string)
			status, _ := inst["status"].(string)
			if idSet[id] && status == "running" {
				running[id] = true
			}
		}
		done := true
		for id := range idSet {
			if !running[id] {
				done = false
				break
			}
		}
		if done {
			return instanceIDs
		}
		time.Sleep(pollInterval)
	}
	all, _ := c.ListInstances()
	var result []string
	for _, inst := range all {
		id, _ := inst["id"].(string)
		status, _ := inst["status"].(string)
		if idSet[id] && status == "running" {
			result = append(result, id)
		}
	}
	if len(result) < len(instanceIDs) {
		log.Printf("[xgc] WaitForRunning 超时: 等待 %d 个，仅 %d 个已 running", len(instanceIDs), len(result))
	}
	return result
}

const maxConcurrentDeploy = 3
const gpuRetryPerModel = 3
const gpuRetryMinInterval = 20 * time.Second
const gpuRetryMaxInterval = 60 * time.Second

// GPU 型号分为两个显存层级，每层级内有正常版和 D 版。
// 回退规则：同层级另一版本 → 更高层级 D 版 → 更高层级正常版。
// 48G 层级无更高层级可回退。
var gpuTiers = [][]string{
	{"NVIDIA GeForce RTX 4090", "NVIDIA GeForce RTX 4090 D"},
	{"NVIDIA GeForce RTX 4090 48G", "NVIDIA GeForce RTX 4090 D 48G"},
}

type DeployResult struct {
	ID       string
	GPUModel string
}

// GPUModelShortName 将 Deploy API 使用的全名转为 ListInstances 返回的短名。
func GPUModelShortName(fullName string) string {
	return strings.TrimPrefix(fullName, "NVIDIA GeForce ")
}

// GPUModelsToTry 根据首选型号构建有序回退列表。
func GPUModelsToTry(primary string) []string {
	tierIdx, posIdx := -1, -1
	for t, tier := range gpuTiers {
		for p, model := range tier {
			if model == primary {
				tierIdx, posIdx = t, p
				break
			}
		}
		if tierIdx >= 0 {
			break
		}
	}
	if tierIdx < 0 {
		return []string{primary}
	}

	models := []string{primary}
	// 同层级另一版本
	sameTier := gpuTiers[tierIdx]
	alt := sameTier[1-posIdx]
	models = append(models, alt)
	// 更高层级（D 版优先，再正常版）
	for t := tierIdx + 1; t < len(gpuTiers); t++ {
		higher := gpuTiers[t]
		models = append(models, higher[1]) // D 版
		models = append(models, higher[0]) // 正常版
	}
	return models
}

func gpuRetryDelay() time.Duration {
	jitter := rand.Int63n(int64(gpuRetryMaxInterval - gpuRetryMinInterval))
	return gpuRetryMinInterval + time.Duration(jitter)
}

// isGPUUnavailable 判断是否为 GPU 不可用（含"可用GPU不足"和"GPU型号暂时不可用"）。
func isGPUUnavailable(err error) bool {
	ae, ok := err.(*APIError)
	if !ok {
		return false
	}
	return strings.Contains(ae.Msg, "可用GPU不足") || strings.Contains(ae.Msg, "GPU型号暂时不可用")
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

type retryConfig struct {
	retriesPerModel int
	delayFn         func() time.Duration
}

var defaultRetryConfig = retryConfig{
	retriesPerModel: gpuRetryPerModel,
	delayFn:         gpuRetryDelay,
}

// deployWithRetry 对单个实例执行带重试和型号回退的部署。
func deployWithRetry(deployFn func(DeployOpts) (string, error), opts DeployOpts, models []string, cfg retryConfig) (DeployResult, error) {
	var id string
	var err error
	var usedModel string

	for mi, model := range models {
		tryOpts := opts
		tryOpts.GPUModel = model
		for attempt := 0; attempt < cfg.retriesPerModel; attempt++ {
			if (mi > 0 || attempt > 0) && cfg.delayFn != nil {
				time.Sleep(cfg.delayFn())
			}
			id, err = deployFn(tryOpts)
			if err == nil {
				usedModel = model
				break
			}
			if !isGPUUnavailable(err) {
				break
			}
		}
		if err == nil || !isGPUUnavailable(err) {
			break
		}
	}

	if err != nil {
		return DeployResult{}, err
	}
	return DeployResult{ID: id, GPUModel: usedModel}, nil
}

// DeployAsync 并发部署 count 个实例。
// allowFallback=true 时按优先级尝试回退型号，每种型号最多重试 gpuRetryPerModel 次；
// allowFallback=false 时仅尝试配置的型号。
func (c *Client) DeployAsync(opts DeployOpts, count int, allowFallback bool) ([]DeployResult, []error) {
	if count <= 0 {
		return nil, nil
	}

	models := []string{opts.GPUModel}
	if allowFallback {
		models = GPUModelsToTry(opts.GPUModel)
	}

	var mu sync.Mutex
	var results []DeployResult
	var errs []error
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentDeploy)

	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, err := deployWithRetry(c.Deploy, opts, models, defaultRetryConfig)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				log.Printf("[xgc] 实例 %d/%d 计划[%s] 部署失败: %v", i+1, count, GPUModelShortName(opts.GPUModel), err)
			} else {
				results = append(results, result)
				if result.GPUModel == opts.GPUModel {
					log.Printf("[xgc] 实例 %d/%d [%s] 部署成功", i+1, count, GPUModelShortName(result.GPUModel))
				} else {
					log.Printf("[xgc] 实例 %d/%d 计划[%s] 实际[%s] 部署成功", i+1, count, GPUModelShortName(opts.GPUModel), GPUModelShortName(result.GPUModel))
				}
			}
		}()
	}
	wg.Wait()
	return results, errs
}

func (c *Client) DestroyAsync(instanceIDs []string) ([]string, []error) {
	var mu sync.Mutex
	var destroyed []string
	var errs []error
	var wg sync.WaitGroup
	for _, id := range instanceIDs {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Destroy(id)
			mu.Lock()
			if err != nil {
				errs = append(errs, err)
			} else {
				destroyed = append(destroyed, id)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return destroyed, errs
}
