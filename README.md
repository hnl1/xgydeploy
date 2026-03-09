# 仙宫云实例自动调度

根据配置的时间与数量限制，自动创建/销毁仙宫云 GPU 实例，支持多份配置、钉钉通知、GitHub Actions 部署。

## 功能

- **定时调度**：到点自动检查实例数量，创建或销毁以达到目标
- **钉钉通知**：执行前发送计划通知，执行后发送结果通知

## 配置示例

```yaml
timezone: "Asia/Shanghai"
configs:
  - image_id: "你的镜像ID"
    image_type: "private"   # 私有镜像填 private，公共镜像填 public（默认 private）
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

**时间段说明**：规则按时间段生效，左闭右开。例如 `10:00 min 6`、`22:00 max 3`：
- `[10:00, 22:00)` 使用 min 6
- `[22:00, 次日 10:00)` 使用 max 3

## 配置加载说明

程序按以下优先级加载配置：

| 优先级 | 方式 | 适用场景 |
|--------|------|----------|
| 1 | `XGC_CONFIG` 环境变量（YAML 内容） | GitHub Actions 等无文件环境，Secrets 存配置 |
| 2 | `XGC_CONFIG_PATH` 环境变量（文件路径） | 本地指定配置文件，如 `configs/config.local.yaml` |
| 3 | 默认 `configs/config.yaml` | 无环境变量时 |

`.env` 文件会在启动时自动加载，用于设置上述环境变量，不改变加载逻辑。

## 部署方式

### 方式一：GitHub Actions（推荐，无需自建服务器）

1. **Fork 或创建仓库**（私有/公开均可）
2. **配置 Secrets**（仓库 Settings → Secrets and variables → Actions → Secrets）：
   - `XGC_API_TOKEN`：仙宫云访问令牌
   - `XGC_CONFIG`：完整 YAML 配置（含镜像 ID、时间、数量等）
   - `DINGTALK_WEBHOOK`：钉钉群机器人 Webhook URL（可选）
   - `DINGTALK_SECRET`：钉钉机器人加签密钥（若启用加签则必填）
3. **定时运行**：工作流已配置为每天 10:00 和 22:00（北京时间）执行
4. **查看配置**：手动触发「查看配置」workflow，将当前 `XGC_CONFIG` 发送到钉钉，方便编辑前查看

**XGC_CONFIG 示例**（复制到 Secrets 的 Value，多行粘贴即可）：
```yaml
timezone: "Asia/Shanghai"
configs:
  - image_id: "你的镜像ID"
    image_type: "private"
    gpu_model: "NVIDIA GeForce RTX 4090"
    gpu_count: 1
    data_center_id: 1
    schedules:
      - time: "10:00"
        min_count: 6
      - time: "22:00"
        max_count: 3
```

当 `XGC_CONFIG` 存在时，程序优先使用它，不再读取 `config.yaml`。编辑前可通过「查看配置」workflow 将当前配置发送到钉钉。也可用命令行更新：

```bash
gh secret set XGC_CONFIG < configs/config.local.yaml
```

### 方式二：自有服务器 / 云函数

```bash
go run ./cmd/xgydeploy       # 执行调度
go run ./cmd/show-config     # 将当前配置发送到钉钉（方便编辑前查看）
```

**本地试验推荐**：创建 `.env`（已加入 .gitignore）和 `configs/config.local.yaml`：

```bash
# .env
XGC_API_TOKEN=你的令牌
XGC_CONFIG_PATH=configs/config.local.yaml
DINGTALK_WEBHOOK=钉钉webhook  # 可选
DINGTALK_SECRET=SEC开头的加签密钥  # 若机器人启用加签则必填
```

```yaml
# configs/config.local.yaml（勿提交，可加入 .gitignore）
timezone: "Asia/Shanghai"
configs:
  - image_id: "你的镜像ID"
    schedules:
      - time: "10:00"
        min_count: 6
      - time: "22:00"
        max_count: 3
```

也可用 `export` 设置环境变量，或直接用 `XGC_CONFIG` 传入完整 YAML 内容。

## 钉钉机器人配置

1. 钉钉群 → 群设置 → 智能群助手 → 添加机器人
2. 选择「自定义」机器人
3. 安全设置：
   - **自定义关键词**：添加如 `实例`、`调度`、`成功`、`失败`
   - **加签**：若选择加签，复制 SEC 开头的密钥，填入 `DINGTALK_SECRET` 环境变量
4. 复制 Webhook 地址，填入 `DINGTALK_WEBHOOK`

## 通知内容示例

每次调度会发送 **两份** 钉钉通知：

### 1. 计划通知（执行前）

```
【仙宫云调度】准备执行

⏰ 时间：2025-03-09 10:00

### 配置 9e5ed458
- 镜像：`9e5ed458...`
- 规则：10:00 最少 6 个
- 当前实例：4 个
- 📥 计划创建：2 个
```

### 2. 结果通知（执行后）

```
【仙宫云调度】执行完成

⏰ 执行时间：2025-03-09 10:00

### 配置 9e5ed458 | ✅ 成功
- 镜像：`9e5ed458...`
- 规则：10:00 最少 6 个
- 操作前：4 个
- 操作后：6 个
- 已创建：2 个
  - `instance-id-1`
  - `instance-id-2`
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
│   ├── scheduler/       # 调度逻辑（Plan + Execute 两阶段）
│   └── notify/          # 钉钉通知（计划 + 结果两份）
├── configs/
│   └── config.yaml
├── go.mod
└── .github/workflows/
    └── schedule.yml
```
