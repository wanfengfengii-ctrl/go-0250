基于 Go 实现的建筑门窗三性能联检 Web 项目，一款后端服务，完成气密、水密与抗风压试验采集、复核及放行。

# windowproof-fenestration-triple-test

## 本地构建与测试

```bash
go mod download
go build ./...
go test ./...
./run_benzhi_smoke.sh
```

## Docker 构建与运行

```bash
./build_benzhi_docker.sh windowproof-fenestration-triple-test linux/arm64
docker run --rm -it --platform linux/arm64 windowproof-fenestration-triple-test:latest
./build_benzhi_docker.sh windowproof-fenestration-triple-test linux/amd64
docker run --rm -it --platform linux/amd64 windowproof-fenestration-triple-test:latest
```

构建脚本第二个参数为目标平台，必须分别完成 linux/arm64 和 linux/amd64 构建与容器验证；未提供时按照规范默认使用 linux/amd64。Dockerfile 不写死 CPU 架构，平台由脚本参数传入。
