package main

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/hnl1/xgydeploy/internal/config"
	"github.com/hnl1/xgydeploy/internal/notify"
)

func main() {
	_ = godotenv.Load()

	raw := config.RawYAML()
	if raw == "" {
		fmt.Fprintln(os.Stderr, "错误: 未找到配置")
		os.Exit(1)
	}
	if notify.SendConfigDingtalk(raw) {
		fmt.Println("配置已发送到钉钉")
	} else {
		fmt.Fprintln(os.Stderr, "发送失败，请检查 DINGTALK_WEBHOOK 配置")
		os.Exit(1)
	}
}
