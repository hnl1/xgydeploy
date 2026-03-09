package xgc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
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
		client: &http.Client{Timeout: 30},
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
	list, _ := data["list"].([]any)
	result := make([]map[string]any, 0, len(list))
	for _, v := range list {
		if m, ok := v.(map[string]any); ok {
			result = append(result, m)
		}
	}
	return result, nil
}

type DeployOpts struct {
	Image        string
	GPUModel     string
	GPUCount     int
	DataCenterID int
	Name         string
}

func (c *Client) Deploy(opts DeployOpts) (string, error) {
	body := map[string]any{
		"image":                  opts.Image,
		"gpu_model":              opts.GPUModel,
		"gpu_count":              opts.GPUCount,
		"data_center_id":        opts.DataCenterID,
		"image_type":             "public",
		"storage":                false,
		"storage_mount_path":     "/root/cloud",
		"system_disk_expand":     false,
		"system_disk_expand_size": 0,
		"name":                   opts.Name,
	}
	data, err := c.request("POST", "/open/instance/deploy", body)
	if err != nil {
		return "", err
	}
	id, _ := data["id"].(string)
	return id, nil
}

func (c *Client) Destroy(instanceID string) error {
	_, err := c.request("POST", "/open/instance/destroy", map[string]any{"id": instanceID})
	return err
}

func (c *Client) DeployAsync(opts DeployOpts, count int) ([]string, []error) {
	var mu sync.Mutex
	var created []string
	var errs []error
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			optsCopy := opts
			optsCopy.Name = fmt.Sprintf("%s-%d", opts.Name, idx)
			id, err := c.Deploy(optsCopy)
			mu.Lock()
			if err != nil {
				errs = append(errs, err)
			} else {
				created = append(created, id)
			}
			mu.Unlock()
		}(i)
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
