# ---- build stage ----
FROM golang:1.24-alpine AS builder

ENV GOPROXY=https://goproxy.cn,direct \
    GO111MODULE=on \
    CGO_ENABLED=0 \
    GOOS=linux

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN go build -ldflags "-w -s -X main.VERSION=${VERSION}" -o /out/tf-api ./cmd/tf-api && \
    go build -ldflags "-w -s -X main.VERSION=${VERSION}" -o /out/tf-worker ./cmd/worker

# ---- runtime stage ----
FROM alpine:3.20

RUN apk --no-cache add tzdata ca-certificates wget && \
    addgroup -g 1001 -S app && \
    adduser -u 1001 -S app -G app

WORKDIR /app

COPY --from=builder /out/tf-api /app/tf-api
COPY --from=builder /out/tf-worker /app/tf-worker
COPY --from=builder /src/configs /app/configs

RUN mkdir -p /app/logs /app/data /app/uploads /app/public && \
    chown -R app:app /app && \
    chmod +x /app/tf-api /app/tf-worker

USER app

EXPOSE 8080

# liveness：只看进程，不查依赖（避免 DB 抖动导致容器被反复重启）
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/tf-api"]
CMD ["web", "-c", "configs/config.yaml"]

# Worker 镜像用法（同一镜像，换启动命令）：
#   docker run --entrypoint /app/tf-worker tf-api:latest -c configs/config.yaml
