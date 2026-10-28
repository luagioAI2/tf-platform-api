.PHONY: help wire build run worker bench test race lint fmt vet tidy swag docker-build docker-run clean check

APP        := tf-api
BIN        := ./cmd/$(APP)
BIN_OUT    := ./$(APP)
WORKER_OUT := ./$(APP)-worker
BENCH_OUT  := ./$(APP)-bench
CONFIG     ?= configs/config.yaml
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -w -s -X main.VERSION=$(VERSION)

help:
	@echo "==================== tf-api ===================="
	@echo "开发:"
	@echo "  make wire         生成 wire 依赖注入代码（改依赖后必跑）"
	@echo "  make run          本地运行 HTTP 服务"
	@echo "  make worker       本地运行 MQ + Cron 进程"
	@echo "  make build        编译全部二进制"
	@echo "  make bench        编译压测工具"
	@echo "质量:"
	@echo "  make fmt          格式化代码"
	@echo "  make vet          静态检查"
	@echo "  make test         单元测试"
	@echo "  make race         竞态检测（需 CGO + gcc）"
	@echo "  make lint         golangci-lint"
	@echo "  make check        fmt + vet + build + test 全套"
	@echo "文档:"
	@echo "  make swag         生成 swagger 文档"
	@echo "容器:"
	@echo "  make docker-build 构建镜像"
	@echo "  make docker-run   运行容器"
	@echo "================================================"

# ============ 开发 ============

wire:
	@echo ">> wire gen"
	@wire gen ./internal/app

run:
	@go run -ldflags "$(LDFLAGS)" $(BIN) web -c $(CONFIG)

worker:
	@go run -ldflags "$(LDFLAGS)" ./cmd/worker -c $(CONFIG)

build:
	@echo ">> build $(VERSION)"
	@CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN_OUT) $(BIN)
	@CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(WORKER_OUT) ./cmd/worker

bench:
	@go build -o $(BENCH_OUT) ./cmd/bench

# ============ 质量 ============

fmt:
	@gofmt -w .

vet:
	@go vet ./...

test:
	@go test -count=1 ./...

# 竞态检测：需要 CGO_ENABLED=1 与可用的 gcc
race:
	@CGO_ENABLED=1 go test -race -count=1 ./...

lint:
	@golangci-lint run ./...

tidy:
	@go mod tidy

check: fmt vet build test
	@echo ">> all checks passed"

# ============ 文档 ============

swag:
	@swag init -g ./cmd/$(APP)/main.go -o ./internal/app/swagger --parseDependency --parseInternal

# ============ 容器 ============

docker-build:
	@docker build --build-arg VERSION=$(VERSION) -t $(APP):$(VERSION) -t $(APP):latest .

docker-run:
	@docker run --rm -p 8080:8080 \
		-e TF_ENV=production \
		-e TF_DB_DSN="$$TF_DB_DSN" \
		-e TF_REDIS_ADDR="$$TF_REDIS_ADDR" \
		-e TF_JWT_KEY="$$TF_JWT_KEY" \
		-e TF_ADMIN_KEY="$$TF_ADMIN_KEY" \
		-e TF_TRUSTED_PROXIES="$$TF_TRUSTED_PROXIES" \
		$(APP):latest web -c configs/config.yaml

clean:
	@rm -rf $(BIN_OUT) $(WORKER_OUT) $(BENCH_OUT) release dist logs data uploads
	@go clean -cache -testcache
