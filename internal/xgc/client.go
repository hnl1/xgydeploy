package xgc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const baseURL = "https://api.xiangongyun.com"

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

func (c *Client) request(method, path string, body any) (map[string]any, error) {
	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
		code, _ := data["code"]
		msg, _ := data["msg"].(string)
		if msg != "" {
			log.Printf("[xgc] deploy 失败: code=%v msg=%q", code, msg)
			return "", fmt.Errorf("%s", msg)
		}
		log.Printf("[xgc] deploy 失败: code=%v 响应缺少 id", code)
		return "", fmt.Errorf("deploy 响应缺少 id")
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

func (c *Client) DeployAsync(opts DeployOpts, count int) ([]string, []error) {
	if count <= 0 {
		return nil, nil
	}
	var mu sync.Mutex
	var created []string
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

			id, err := c.Deploy(opts)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				log.Printf("[xgc] 创建实例 %d/%d 失败: %v", i+1, count, err)
			} else {
				created = append(created, id)
				log.Printf("[xgc] 创建实例 %d/%d 成功", i+1, count)
			}
		}()
	}
	wg.Wait()
	return created, errs
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
