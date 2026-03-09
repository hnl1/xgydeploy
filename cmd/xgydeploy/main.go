package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/hnl1/xgydeploy/internal/config"
	"github.com/hnl1/xgydeploy/internal/notify"
	"github.com/hnl1/xgydeploy/internal/scheduler"
	"github.com/hnl1/xgydeploy/internal/xgc"
)

func main() {
	_ = godotenv.Load() // 加载 .env，文件不存在时忽略

	timezone, configs, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	now := time.Now()
	timeStr := now.Format("2006-01-02 15:04")

	fmt.Printf("[%s] 开始调度 (时区: %s)\n", timeStr, timezone)
	fmt.Printf("配置数量: %d\n", len(configs))

	client, err := xgc.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	log.Println("[main] 开始执行调度")
	results := scheduler.Run(client, configs, timezone, now)
	log.Printf("[main] 调度完成，处理 %d 个配置", len(results))

	if len(results) == 0 {
		fmt.Println("当前时间无匹配的调度规则，跳过")
		return
	}

	for _, r := range results {
		status := "成功"
		if !r.Success {
			status = "失败"
		}
		fmt.Printf("  [%s] %s | %d -> %d\n", r.ConfigKey, status, r.BeforeCount, r.AfterCount)
		if len(r.Created) > 0 {
			fmt.Printf("    创建: %v\n", r.Created)
		}
		if len(r.Destroyed) > 0 {
			fmt.Printf("    销毁: %v\n", r.Destroyed)
		}
		if r.Error != "" {
			fmt.Printf("    错误: %s\n", r.Error)
		}
	}

	if os.Getenv("DINGTALK_WEBHOOK") != "" {
		if notify.SendDingtalk(results, timeStr) {
			fmt.Println("钉钉通知: 已发送")
		} else {
			fmt.Println("钉钉通知: 发送失败")
		}
	} else {
		fmt.Println("未配置 DINGTALK_WEBHOOK，跳过钉钉通知")
	}
}
