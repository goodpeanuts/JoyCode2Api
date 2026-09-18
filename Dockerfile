# 构建时可通过 --build-arg 切换源，解决部分网络环境连接失败的问题：
#   ALPINE_MIRROR  Alpine 包源（国内示例：https://mirrors.aliyun.com/alpine）
#   GOPROXY        Go 模块代理（国内示例：https://goproxy.cn,direct）
#   VERSION        版本号，注入二进制的 version 输出（默认 dev）
ARG ALPINE_MIRROR=https://dl-cdn.alpinelinux.org/alpine

FROM golang:1.25-alpine AS builder

ARG ALPINE_MIRROR
ARG GOPROXY=""
ARG VERSION=dev
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates

WORKDIR /app

ENV CGO_ENABLED=0
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath \
    -ldflags "-s -w -X main.Version=${VERSION}" \
    -o /jcproxy ./cmd/JoyCodeProxy

FROM alpine:3.19

ARG ALPINE_MIRROR
RUN sed -i "s|https://dl-cdn.alpinelinux.org/alpine|${ALPINE_MIRROR}|g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates

COPY --from=builder /jcproxy /usr/local/bin/jcproxy

EXPOSE 34891

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:34891/health || exit 1

ENTRYPOINT ["jcproxy"]
# 容器内无 JoyCode 凭据，默认跳过启动校验（可在 compose 中覆盖）。
CMD ["serve", "--skip-validation"]
