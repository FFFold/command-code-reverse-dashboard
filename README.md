# command-code-reverse-dashboard

`commandcode-proxy` 的余额看板：可视化 key 余额、限额窗口、告警、Prometheus
指标与健康/版本。单个 Go 二进制，零第三方依赖，前端由 `go:embed` 内嵌。

## 快速开始

```bash
cp .env.example .env   # 填入 PROXY_BASE_URL 和 PROXY_API_KEY
go run ./cmd/credit-dashboard
# 打开 http://localhost:8787
```

## 配置

全部通过环境变量（支持 `.env`）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `PROXY_BASE_URL` | `http://localhost:3050` | 目标代理地址 |
| `PROXY_API_KEY` | — (必填) | 调用 `/v1/credits` 的下游 key，仅服务端持有 |
| `HOST` / `PORT` | `0.0.0.0` / `8787` | 监听地址（无内置鉴权，请自行防护） |
| `ALERT_WINDOW_PERCENT` | `80` | 窗口用量告警阈值（百分比） |
| `ALERT_LOW_CREDIT_USD` | `5` | 低余额告警阈值（美元） |
| `UPSTREAM_TIMEOUT_SECONDS` | `10` | 请求代理的超时 |
| `LOG_LEVEL` | `info` | 日志级别 |

## 余额口径

- 后端调用 `GET /v1/credits`（`Authorization: Bearer <PROXY_API_KEY>`）。
- **剩余余额 = `monthlyCredits + purchasedCredits + freeCredits`**；
  `monthlyCredits` 已是月度剩余，窗口 `used` 不可重复计入。
- 用户手动点击「刷新」触发；无自动轮询。

## 接口

- `GET /api/summary` — 余额、窗口、订阅、健康、版本、告警（各源失败时部分降级）。
- `GET /api/metrics` — 解析后的 `/metrics` 数据。
- `GET /` — 内嵌前端。

## Docker

```bash
docker build -t credit-dashboard .
docker run --rm -p 8787:8787 \
  -e PROXY_BASE_URL=http://<proxy-host>:3050 \
  -e PROXY_API_KEY=<key> \
  credit-dashboard
```

镜像发布在 GHCR：`ghcr.io/fffold/command-code-reverse-dashboard`
（`edge` 跟随 main，`latest` 跟随版本 tag；多架构 amd64/arm64）。

## 开发

```bash
go vet ./... && go test -race ./...
```
