# 仙宫云实例自动调度

根据配置的时间与数量限制，自动创建/销毁仙宫云 GPU 实例，支持多份配置、钉钉通知、GitHub Actions 部署。

## 功能

- **定时调度**：到点自动检查实例数量，创建或销毁以达到目标
- **多配置**：支持多份配置，每份可指定不同镜像、时间与数量
- **智能销毁**：多于目标时按创建时间排序，优先销毁最早创建的
- **钉钉通知**：每次执行完成后发送详细报告

## 配置示例

```yaml
timezone: "Asia/Shanghai"
configs:
  - image_id: "你的镜像ID"
    gpu_model: "NVIDIA GeForce RTX 4090"
    gpu_count: 1
    data_center_id: 1
    schedules:
      - time: "10:00"
        min_count: 6   # 10:00 至少 6 个
      - time: "22:00"
        max_count: 3   # 22:00 最多 3 个

  - image_id: "另一个镜像id"
    schedules:
      - time: "08:00"
        min_count: 4
      - time: "20:00"
        max_count: 2
```

**保密建议**：将完整配置放入 `XGC_CONFIG` 环境变量（GitHub Secrets），镜像 ID 等敏感信息不写入仓库。

## 部署方式

### 方式一：GitHub Actions（推荐，无需自建服务器）

1. **Fork 或创建仓库**（私有/公开均可）
2. **配置 Secrets**（仓库 Settings → Secrets and variables → Actions）：
   - `XGC_API_TOKEN`：仙宫云访问令牌
   - `XGC_CONFIG`：**完整 YAML 配置**（含镜像 ID、时间、数量等，保密不提交）
   - `DINGTALK_WEBHOOK`：钉钉群机器人 Webhook URL（可选）
3. **定时运行**：工作流已配置为每天 10:00 和 22:00（北京时间）执行

**XGC_CONFIG 示例**（复制到 Secrets 的 Value，多行粘贴即可）：
```yaml
timezone: "Asia/Shanghai"
configs:
  - image_id: "你的镜像ID"
    gpu_model: "NVIDIA GeForce RTX 4090"
    gpu_count: 1
    data_center_id: 1
    schedules:
      - time: "10:00"
        min_count: 6
      - time: "22:00"
        max_count: 3
```

当 `XGC_CONFIG` 存在时，程序优先使用它，不再读取 `config.yaml`，镜像 ID 等敏感信息可完全保密。

### 方式二：自有服务器 / 云函数

```bash
go build -o xgydeploy .
export XGC_API_TOKEN="你的令牌"
export XGC_CONFIG="timezone: Asia/Shanghai
configs:
  - image_id: 你的镜像ID
    schedules:
      - time: \"10:00\"
        min_count: 6
      - time: \"22:00\"
        max_count: 3"
export DINGTALK_WEBHOOK="钉钉webhook"  # 可选
./xgydeploy
# 或用 cron 定时执行
```

## 钉钉机器人配置

1. 钉钉群 → 群设置 → 智能群助手 → 添加机器人
2. 选择「自定义」机器人
3. 安全设置选择「自定义关键词」，添加如：`实例`、`调度`、`成功`、`失败`
4. 复制 Webhook 地址，填入 GitHub Secrets 的 `DINGTALK_WEBHOOK`

## 通知内容示例

```
【仙宫云调度】执行完成

📋 配置：镜像 9e5ed4...e0b1
⏰ 时间：10:00 | 目标：最少 6 个

✅ 操作：创建 2 个新实例
📊 当前：6 个实例（目标 6）
```

## 实例归属说明

本工具通过**实例名称前缀** `xgydeploy-{镜像ID前8位}` 识别自己创建的实例。新建实例会自动带上该前缀。若仙宫云 API 返回实例的 `image`/`image_id` 字段，则也会按镜像 ID 匹配已有实例。

## 项目结构

```
xgydeploy/
├── cmd/
│   └── xgydeploy/
│       └── main.go      # 入口
├── internal/
│   ├── config/          # 配置加载
│   ├── xgc/             # 仙宫云 API 客户端（并发创建/销毁）
│   ├── scheduler/       # 调度逻辑
│   └── notify/          # 钉钉通知
├── configs/
│   └── config.yaml
├── go.mod
└── .github/workflows/
    └── schedule.yml
```
