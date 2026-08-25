# SpecWire Bridge 容器镜像（多阶段构建）
# builder：编译 Go 二进制（modernc sqlite 纯 Go，无 CGO）
# runtime：alpine + multica CLI（linux/arm64，官方 GitHub release）

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/specwire-bridge .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates curl tar
ARG MULTICA_VERSION=0.4.29
RUN curl -fsSL -o /tmp/multica.tar.gz \
      "https://github.com/multica-ai/multica/releases/download/v${MULTICA_VERSION}/multica-cli-${MULTICA_VERSION}-linux-arm64.tar.gz" \
    && tar -xzf /tmp/multica.tar.gz -C /usr/local/bin \
    && rm /tmp/multica.tar.gz \
    && multica --version

COPY --from=builder /out/specwire-bridge /usr/local/bin/specwire-bridge

# CLI 从这里找 profile：$HOME/.multica/profiles/<name>（compose 挂载只读）
ENV HOME=/multica \
    SPECWIRE_DB_PATH=/app/data/specwire-bridge.db
WORKDIR /app
EXPOSE 8787
CMD ["specwire-bridge"]
