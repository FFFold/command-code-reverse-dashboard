# Credit Dashboard 设计文档

日期：2026-09-11
状态：已确认，待实现

## 1. 背景与目标

`command-code-reverse` 是一个 Go 代理，把 Command Code 的 `/alpha/generate`
包装成 OpenAI 兼容接口，并暴露 `GET /v1/credits`（透传上游 billing 数据）。

本项目 `command-code-reverse-dashboard` 是一个独立的、单二进制的可视化 dashboard，
目标：

- **核心**：可视化代理账户的 key 余额与限额窗口。
- **附加**：阈值告警提示、Prometheus 指标面板、代理健康与版本。

数据来源为运行中的代理实例。非目标：历史趋势、消耗预测、模型目录、多目标管理、
dashboard 自身鉴权（超出本期范围）。

## 2. 数据语义（已实测确认）

通过真实实例 `http://100.87.49.15:9511` 验证。发一次 chat completion 请求前后，
`/v1/credits` 返回如下变化：

| 字段 | 请求前 | 请求后 | 增量 |
|---|---|---|---|
| `credits.monthlyCredits` | 9.691217082 | 9.679693662 | −0.01152342 |
| `credits.windowLimits.fiveHour.used` | 0.093763136 | 0.105286556 | +0.01152342 |
| `credits.windowLimits.weekly.used` | 0.308782918 | 0.320306338 | +0.01152342 |

且 `monthlyCredits + weekly.used` 恒为套餐总额（本例 `individual-go` = 10.0）。

**结论：**

- `monthlyCredits` = 月度**剩余**额度（已扣除当月已用），**不是**总额。
- `windowLimits.*.used / cap` = 5 小时 / 每周**滚动窗口**的已用与上限，独立限流。
- **剩余余额 = `monthlyCredits + purchasedCredits + freeCredits`**。
  窗口 `used` 已包含在 `monthlyCredits` 的扣减中，**不得**再与余额相加（会重复计算）。

### 真实响应结构（两层嵌套）

```json
{
  "credits": {
    "credits": {
      "belowThreshold": false,
      "creditThreshold": 0,
      "monthlyCredits": 9.679693662,
      "purchasedCredits": 0,
      "freeCredits": 0
    },
    "windowLimits": {
      "limited": true,
      "exceeded": null,
      "fiveHour": { "used": 0.105286556, "cap": 3, "exceeded": false, "resetAt": 1789116010242 },
      "weekly":   { "used": 0.320306338, "cap": 6, "exceeded": false, "resetAt": 1789213109428 }
    }
  },
  "subscriptions": {
    "success": true,
    "data": {
      "planId": "individual-go",
      "status": "active",
      "currentPeriodStart": "2026-09-05T11:30:04Z",
      "currentPeriodEnd": "2026-10-05T11:30:04Z"
    }
  }
}
```

- `resetAt` 为 epoch **毫秒**。
- 上游若返回 `credits.credits` 缺失，解析需容错（视同 0）。

## 3. 架构

单 Go 二进制，零第三方依赖（标准库 + `embed`），与父项目理念一致。

```
浏览器 (index.html + app.js + style.css，经 go:embed 内嵌)
   │  GET /api/summary      GET /api/metrics      （仅手动刷新触发）
   ▼
Go dashboard 后端（持有 PROXY_API_KEY，仅服务端可见）
   │  GET /v1/credits                     (Bearer 鉴权)
   │  GET /metrics /healthz /readyz /version  (无需鉴权)
   ▼
commandcode-proxy
```

关键约束：

- 代理密钥**只存服务端**，绝不下发浏览器。
- 后端对代理的每次请求带超时（`UPSTREAM_TIMEOUT_SECONDS`，默认 10s）。
- 任一上游调用失败时，返回结构化错误并尽力返回其余可用的数据（部分降级）。

## 4. 后端接口

### `GET /api/summary`

一次抓取并聚合以下数据：

- `credits`：`monthlyCredits` / `purchasedCredits` / `freeCredits` /
  `belowThreshold` / `creditThreshold`。
- `balance`：服务端计算的 `monthlyCredits + purchasedCredits + freeCredits`。
- `windows`：`fiveHour`、`weekly` 的 `used` / `cap` / `exceeded` / `resetAt`。
- `subscriptions`：`planId` / `status` / 账期起止。
- `health`：`/healthz`、`/readyz` 的连通结果与 HTTP 状态。
- `version`：`/version` 的 `version`（代理）与 `ccVersion`（上游 CLI）。
- `alerts`：服务端按阈值评估出的告警数组（见 §6）。
- `fetchedAt`：服务端抓取完成时间（RFC3339）。
- `errors`：各上游调用失败时的 `{source, message}`（用于部分降级展示）。

`/v1/credits` 与 `/version` 需 Bearer（`/version` 实测无需鉴权，但统一携带无副作用）；
`/healthz`、`/readyz`、`/metrics` 无需鉴权。

### `GET /api/metrics`

服务端抓取代理 `/metrics`，将 Prometheus 文本解析为 JSON：

- `requests`：按 `model` / `stream` / `result` 的计数，含总计。
- `tokens`：按 `kind`（input / output / cached）的总数。
- `costUSD`：`cost_microdollars_total` 求和并换算为美元；含分模型明细。
- `latency`：`latency_ms` 的 count / sum / 平均（按 model/stream）。
- `upstreamErrors`：按 class 的计数。

若 `METRICS_ENABLED=false`（代理返回 404/非文本），前端显示「指标未启用」。

### 静态资源

- `GET /` → `index.html`
- `GET /assets/*` → 内嵌的 `app.js`、`style.css`

## 5. 前端（布局 A：英雄数字）

- **顶部**：大号「剩余可用」`$balance`；下方并排 5H / 每周两条进度条，
  显示 `used / cap`、占用百分比、`resetAt` 本地时间与倒计时；超阈值变色。
- **明细**：月度剩余 / 已购 / 赠送三张小卡；`belowThreshold` 状态；
  套餐 `planId` / `status` / 账期。
- **指标面板**：请求总数、token 三分项、成本美元、平均延迟、结果分布。
- **健康与版本**：`/healthz`、`/readyz` 状态徽章；代理版本 + CLI 版本。
- **告警列表**：服务端评估的告警（无告警时显示「正常」）。
- 交互：仅手动「刷新」按钮 + 「最后更新」时间；加载态、错误态（部分降级时展示可用区块）。
- 深色主题（同 mockup），响应式，无构建步骤（vanilla HTML/CSS/JS）。

## 6. 告警规则

服务端评估，前端只渲染结果：

- `window_usage`：任一窗口 `used / cap * 100 >= ALERT_WINDOW_PERCENT`（默认 80）。
  严重级（critical）当 `exceeded == true` 或占用 ≥ 95%。
- `low_credit`：`balance <= ALERT_LOW_CREDIT_USD`（默认 5）。
- `below_threshold`：上游 `belowThreshold == true`。
- `proxy_unhealthy`：`/healthz` 或 `/readyz` 非 200、或请求失败。
- 每条告警含 `level`（info / warning / critical）、`title`、`detail`。

## 7. 配置（环境变量，支持 `.env`，真实环境变量优先）

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `PROXY_BASE_URL` | | `http://localhost:3050` | 目标代理地址 |
| `PROXY_API_KEY` | ✅ | — | 调用 `/v1/credits` 的下游 key，仅服务端持有 |
| `HOST` / `PORT` | | `0.0.0.0` / `8787` | dashboard 监听地址 |
| `ALERT_WINDOW_PERCENT` | | `80` | 窗口用量告警阈值（百分比） |
| `ALERT_LOW_CREDIT_USD` | | `5` | 低余额告警阈值（美元） |
| `UPSTREAM_TIMEOUT_SECONDS` | | `10` | 后端请求代理的超时 |
| `LOG_LEVEL` | | `info` | debug / info / warn / error |

无 dashboard 自身鉴权，可绑定任意地址（由部署方用防火墙 / 反代保护）。

## 8. 错误处理与降级

- 单个上游调用失败不影响其余区块：`/api/summary` 返回已获取的数据 +
  `errors[]` 列表，前端对应区块显示错误占位。
- `PROXY_API_KEY` 无效（`/v1/credits` 401）→ `errors` 中标注「代理鉴权失败」。
- 代理不可达 / 超时 → 健康区块标记 down，`/api/summary` 仍返回结构与 `errors`。
- `/metrics` 解析对未知指标行容错（跳过，不报错）。

## 9. 测试

Go（`go vet ./... && go test -race ./...`）：

- `httptest` mock 代理，覆盖：
  - credits 双层嵌套解析、缺失字段容错、`monthlyCredits` 语义（余额计算不含窗口 used）。
  - metrics 文本解析（counter / summary、多标签、未知行跳过）。
  - 各告警规则（窗口百分比、critical、低余额、belowThreshold、不健康）。
  - 上游失败 / 超时的部分降级与 `errors[]`。
  - 静态资源与 `/` 返回。
- 可选：对真实实例 `http://100.87.49.15:9511` 的手动 smoke 脚本（不进 CI）。

## 10. 项目结构

```
command-code-reverse-dashboard/
├── cmd/credit-dashboard/main.go      # 入口、路由、优雅退出
├── internal/
│   ├── config/config.go              # 环境变量 / .env 加载与校验
│   ├── upstream/client.go            # 代理 API 客户端（credits/version/health/metrics）
│   ├── credits/parse.go              # credits 响应解析 + 余额计算
│   ├── promparse/parse.go            # Prometheus 文本解析
│   └── alerts/alerts.go              # 阈值评估
├── web/                              # go:embed 的静态前端
│   ├── index.html
│   ├── app.js
│   └── style.css
├── docs/superpowers/specs/           # 本设计文档
├── .gitignore                        # 已含 .superpowers/
└── LICENSE
```

## 11. 验证标准

- `go build ./...`、`go vet ./...`、`go test -race ./...` 全绿。
- 用真实实例启动 dashboard，`/api/summary` 正确显示余额、双窗口、健康、版本。
- 手动刷新可观察到请求后余额下降、窗口 used 上升。
- 关闭代理后 dashboard 仍能打开并显示错误降级态。
