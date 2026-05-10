# heartbeat-watch-go

`heartbeat-watch-go` 是一个只依赖 Go 标准库的心跳监控演示服务。它维护一组服务、接收服务心跳、记录心跳历史，并在失败或超时未上报时打开 incident。

## 功能

- 服务注册、查询、更新和删除。
- 心跳上报，支持 `ok` / `warn` / `fail` 状态。
- `fail` 心跳自动打开 `heartbeat_failed` incident。
- dashboard 会检查超时服务并标记为 `missed`。
- incident 列表查询与幂等 resolve。

## 数据模型

### Service

- `id`: 3-32 位小写字母、数字或短横线，必须以小写字母开头。
- `name`: 服务名称。
- `owner`: 负责人，未传时为 `unowned`。
- `tags`: 小写标签，会去重。
- `interval_seconds`: 心跳间隔，默认 `60`。
- `timeout_seconds`: 超时宽限，默认 `interval_seconds * 2`。
- `status`: `unknown`、`ok`、`warn`、`fail`、`missed`。
- `enabled`: 禁用服务不能上报心跳。

### Heartbeat

- `status`: 空值按 `ok` 处理，可取 `ok`、`warn`、`fail`。
- `message`: 上报消息。
- `latency_ms`: 非负整数。
- `metadata`: 可选对象。

### Incident

- `status`: `open` 或 `resolved`。
- `reason`: `heartbeat_failed` 或 `heartbeat_missed`。

## 运行

```bash
go test ./...
PORT=8801 go run .
```

Docker:

```bash
docker build -t heartbeat-watch-go .
docker run --rm -p 8801:8801 heartbeat-watch-go
curl http://127.0.0.1:8801/health
```

## API 示例

```bash
curl http://127.0.0.1:8801/services

curl -X POST http://127.0.0.1:8801/services \
  -H 'content-type: application/json' \
  -d '{"id":"search-api","name":"Search API","owner":"search@example.test","tags":["edge"],"interval_seconds":30}'

curl -X POST http://127.0.0.1:8801/services/search-api/heartbeat \
  -H 'content-type: application/json' \
  -d '{"status":"ok","message":"green","latency_ms":15}'

curl http://127.0.0.1:8801/services/search-api/heartbeats?limit=5
curl http://127.0.0.1:8801/dashboard
curl http://127.0.0.1:8801/incidents?status=open
```

## 默认数据

启动时会加载两个默认服务:

- `api-gateway`: `ok` heartbeat，标签 `edge` / `critical`。
- `billing-worker`: `warn` heartbeat，标签 `billing`。
