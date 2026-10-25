# syntax=docker/dockerfile:1

# ---------- 构建阶段 ----------
FROM golang:1.26-alpine AS builder

# 国内网络默认走 goproxy.io；可在 docker compose build 时覆盖
ARG GOPROXY=https://goproxy.io,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src

# 先复制依赖清单，命中缓存时无需重复下载
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/api \
      ./cmd/api

# ---------- 运行阶段 ----------
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/api /app/api

# 以非 root 用户运行
USER app

EXPOSE 8080

ENTRYPOINT ["/app/api"]
