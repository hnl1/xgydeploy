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
	_ = godotenv.Load()

	timezone, configs, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	tz, err := time.LoadLocation(timezone)
	if err != nil {
		tz = time.UTC
	}
	now := time.Now().In(tz)
	timeStr := now.Format("2006-01-02 15:04")

	fmt.Printf("[%s] 开始调度 (时区: %s)\n", timeStr, timezone)
	fmt.Printf("配置数量: %d\n", len(configs))

	client, err := xgc.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	// Phase 1: 分析计划
	log.Println("[main] 分析调度计划")
	plans, err := scheduler.Plan(client, configs, timezone, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	if len(plans) == 0 {
		fmt.Println("当前时间无匹配的调度规则，跳过")
		return
	}

	hasDingtalk := os.Getenv("DINGTALK_WEBHOOK") != ""

	var actionPlans []scheduler.ActionPlan
	for _, p := range plans {
		if p.Count > 0 {
			actionPlans = append(actionPlans, p)
		}
	}
	fmt.Printf("匹配规则: %d 个配置，%d 个需要操作\n", len(plans), len(actionPlans))

	// 计划通知始终发送
	if hasDingtalk {
		balance, _ := client.GetBalance()
		if notify.SendPlanDingtalk(plans, timeStr, balance) {
			fmt.Println("钉钉通知: 计划已发送")
		} else {
			fmt.Println("钉钉通知: 计划发送失败")
		}
	}

	if len(actionPlans) == 0 {
		fmt.Println("所有配置已满足要求，无需操作")
		return
	}

	// Phase 2: 只执行需要操作的计划
	log.Println("[main] 开始执行调度")
	results := scheduler.Execute(client, actionPlans)
	log.Printf("[main] 调度完成，处理 %d 个配置", len(results))

	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}
	fmt.Printf("执行完成: %d 成功, %d 失败\n", successCount, len(results)-successCount)

	if hasDingtalk {
		balance, _ := client.GetBalance()
		if notify.SendResultDingtalk(results, timeStr, balance) {
			fmt.Println("钉钉通知: 结果已发送")
		} else {
			fmt.Println("钉钉通知: 结果发送失败")
		}
	} else {
		fmt.Println("未配置 DINGTALK_WEBHOOK，跳过钉钉通知")
	}
}
