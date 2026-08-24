# BENZHI 打包说明

## 构建
```bash
./build_benzhi_docker.sh seiswatch-run linux/amd64
./build_benzhi_docker.sh seiswatch-run linux/arm64
```

镜像名规范：`benzhi/<name>:latest`，name 由调用方传入。

## 运行
```bash
docker run --rm -p 8080:8080 -v $(pwd)/data:/app/data benzhi/seiswatch-run:latest
```

容器内使用 `golang:1.22-bookworm` 基础镜像，`GOTOOLCHAIN=local` 钉死工具链，
依赖在构建阶段已通过 goproxy.cn 拉取并缓存进镜像层。
