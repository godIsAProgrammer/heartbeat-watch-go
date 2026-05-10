# heartbeat-watch-go 环境说明

## 基本环境

- 语言: Go 1.23
- 依赖: Go 标准库
- 默认端口: `8801`
- 启动入口: `main.go`
- HTTP 服务: `watch/server.go`
- 内存状态: `watch/store.go`

## 本机验证

```bash
cd heartbeat-watch-go
go test ./...
PORT=8801 go run .
```

健康检查:

```bash
curl http://127.0.0.1:8801/health
```

业务接口验证:

```bash
curl http://127.0.0.1:8801/services
curl -X POST http://127.0.0.1:8801/services \
  -H 'content-type: application/json' \
  -d '{"id":"search-api","name":"Search API","owner":"search@example.test","tags":["edge"],"interval_seconds":30,"timeout_seconds":60}'
curl -X POST http://127.0.0.1:8801/services/search-api/heartbeat \
  -H 'content-type: application/json' \
  -d '{"status":"fail","message":"down","latency_ms":99}'
curl http://127.0.0.1:8801/incidents?status=open
curl http://127.0.0.1:8801/dashboard
```

## Docker 验证

```bash
docker build -t heartbeat-watch-go heartbeat-watch-go
docker run --rm -d -p 8801:8801 --name heartbeat-watch-qc heartbeat-watch-go
curl http://127.0.0.1:8801/health
docker exec heartbeat-watch-qc pwd
docker exec heartbeat-watch-qc git status --short
docker exec heartbeat-watch-qc go test ./...
docker stop heartbeat-watch-qc
```

构建阶段会先执行 `go test ./...`，随后在容器 `/app` 内初始化 Git 仓库并提交初始 fixture。
