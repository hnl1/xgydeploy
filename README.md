# 仙宫云实例自动调度

根据配置的时间与数量限制，自动创建/销毁仙宫云 GPU 实例，支持多份配置、GPU 型号智能回退、钉钉通知、GitHub Actions 部署。

## 功能

- **每小时调度**：每小时检查实例数量和 GPU 型号，创建、销毁或替换以达到目标
- **GPU 型号回退**：GPU 不足时按优先级自动尝试备选型号
- **自动替换**：实例数量满足但存在回退型号时，尝试部署首选型号并替换回退实例
- **钉钉通知**：仅在需要操作时发送执行报告（含计划情况和实际执行结果），无需操作时不打扰

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

## GPU 型号回退机制

当首选 GPU 型号不足时，系统会按优先级自动尝试备选型号，每种型号最多重试 3 次（随机等待 20-60 秒）。

**支持的 4 种型号**：

仙宫云的 RTX 4090 系列分为两个显存层级，每个层级有正常版和 D 版：

- **24G 层级**：`RTX 4090`、`RTX 4090 D`
- **48G 层级**：`RTX 4090 48G`、`RTX 4090 D 48G`

**回退优先级规则**：

1. 首先尝试配置中指定的型号
2. 然后尝试同显存层级的另一版本（正常版 ↔ D 版）
3. 最后尝试更高显存层级（D 版优先于正常版）
4. 48G 层级已是最高，无更高层级可回退，仅在同层级内切换

例如：配置 `RTX 4090` 时，回退顺序为 4090 → 4090 D → 4090 D 48G → 4090 48G；配置 `RTX 4090 48G` 时，仅尝试 4090 48G → 4090 D 48G。

**调度行为**：

- **数量不足**：创建实例，允许回退到备选型号
- **数量满足但存在回退型号**：尝试部署首选型号，每成功一台即销毁一台回退实例（按回退优先级反序销毁）
- **数量超标**：销毁多余实例，优先销毁回退型号

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
3. **定时运行**：工作流每小时执行一次（第 13 分钟），检查数量和 GPU 型号
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

仅在需要操作时发送一份执行报告，包含计划情况和实际执行结果；若所有配置已满足要求，不发送任何通知：

```
【仙宫云调度】执行报告

⏰ 时间：2025-03-09 10:00
💰 账户余额：188.00 元

### 我的镜像 | ✅ 成功

**计划情况**
- 规则：07:00 最少 6 个
- 首选型号：RTX 4090
- 执行前实例：6 个（首选 2 / 回退 4）
- 回退分布：RTX 4090 D×2 / RTX 4090 D 48G×2
- 计划操作：🔄 替换 4 个 → RTX 4090

**执行结果**
- 执行后实例：6 个
- 已创建：3 个
- 实际型号：RTX 4090×3
- 已替换：3 个
- 已销毁：3 个
```

## 实例归属说明

本工具通过**实例名称前缀** `xgydeploy-{镜像ID前8位}` 识别自己创建的实例。新建实例会自动带上该前缀。若仙宫云 API 返回实例的 `image`/`image_id` 字段，则也会按镜像 ID 匹配已有实例。

## 项目结构

```
xgydeploy/
├── cmd/
│   ├── xgydeploy/
│   │   └── main.go      # 入口
│   └── show-config/
│       └── main.go      # 查看配置工具
├── internal/
│   ├── config/          # 配置加载
│   ├── xgc/             # 仙宫云 API 客户端（并发创建/销毁、GPU 型号回退）
│   ├── scheduler/       # 调度逻辑（Plan + Execute、create/destroy/replace）
│   └── notify/          # 钉钉通知（执行报告、型号分布）
├── configs/
│   └── config.yaml
├── go.mod
└── .github/workflows/
    ├── schedule.yml      # 每小时调度
    └── show-config.yml   # 查看配置
```
