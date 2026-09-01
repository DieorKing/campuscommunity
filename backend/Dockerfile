# campuscommunity 后端镜像：多阶段构建
# 阶段一用 golang 完整工具链编译出静态二进制；阶段二仅拷贝二进制进 alpine——
# 最终镜像不含 Go 工具链与源码（体积 ~1GB → ~20MB），且缩小攻击面。
# 构建上下文 = backend/ 目录（docker compose / docker build 的执行位置见 compose 配置）

# ---------- 阶段一：编译 ----------
FROM golang:1.26-alpine AS builder

WORKDIR /build

# 先只拷贝 go.mod/go.sum 并下载依赖：依赖未变时该层命中缓存，
# 源码改动不触发重新下载（分层缓存是 Docker 构建提速的核心手段）
COPY go.mod go.sum ./
RUN go mod download

# 再拷贝源码并编译
COPY . .

# CGO_ENABLED=0 纯静态编译：二进制不依赖系统 libc，alpine(musl) 上可裸跑
# GOOS=linux 目标平台为容器 OS（宿主机是 Windows，交叉编译必须显式指定）
# -trimpath -ldflags="-s -w" 去符号表与路径信息：减小体积、不泄露构建机路径
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o server ./cmd

# ---------- 阶段二：运行 ----------
FROM alpine:3.20

# tzdata：MySQL DSN loc=Local 与日志时间戳依赖本地时区，容器默认 UTC
# ca-certificates：标准安全基线（当前无外呼 HTTPS，为依赖演进预留）
RUN apk add --no-cache tzdata ca-certificates

ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=builder /build/server /app/server

# 配置文件不进镜像（12-factor：配置与制品分离）——由 docker-compose
# 挂载 /app/config/，改配置无需重建镜像
EXPOSE 8080

# ENTRYPOINT 固定二进制，CMD 提供默认参数：docker run 追加参数可覆盖 CMD
ENTRYPOINT ["/app/server"]
CMD ["-f", "/app/config/config.container.yaml"]
