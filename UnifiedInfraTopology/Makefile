.DEFAULT_GOAL := help
GO ?= go
CONF ?= config/local.yml

.PHONY: help init run worker migrate test coverage race vet build wire mock swag
help:
	@printf '%s\n' \
	  'run        启动 API（不迁移；CONF=config/local.yml）' \
	  'worker     启动独立 worker（仅待机，不领取任务）' \
	  'migrate    显式执行目标数据库迁移；运行前核对 CONF / APP_CONF' \
	  'test       运行全部测试；不启用真实图库检查' \
	  'coverage   运行全部测试并生成覆盖率报告' \
	  'race       运行全部竞态测试' \
	  'vet        运行 go vet' \
	  'build      编译 server、migration 和 worker 到 bin/' \
	  'wire       重新生成现有入口的 Wire 装配' \
	  'mock       重新生成用户服务和仓储 mocks' \
	  'swag       重新生成现有 Swagger 注解' \
	  'init       安装与 go.mod 一致版本的开发工具'

init:
	$(GO) install github.com/google/wire/cmd/wire@v0.7.0
	$(GO) install github.com/golang/mock/mockgen@v1.6.0
	$(GO) install github.com/swaggo/swag/cmd/swag@v1.16.6

run:
	$(GO) run ./cmd/server -conf "$(CONF)"

worker:
	$(GO) run ./cmd/worker -conf "$(CONF)"

# 不与 run/build/test 隐式关联，必须单独执行。
migrate:
	$(GO) run ./cmd/migration -conf "$(CONF)"

test:
	INVENTORY_GRAPH_TEST_CONFIG= $(GO) test ./... -count=1 -timeout=90s

coverage:
	INVENTORY_GRAPH_TEST_CONFIG= $(GO) test ./... -coverprofile=coverage.out -count=1 -timeout=90s
	$(GO) tool cover -html=coverage.out -o coverage.html

race:
	INVENTORY_GRAPH_TEST_CONFIG= $(GO) test -race ./... -count=1 -timeout=90s

vet:
	$(GO) vet ./...

build:
	$(GO) build -ldflags="-s -w" -o ./bin/server ./cmd/server
	$(GO) build -ldflags="-s -w" -o ./bin/migration ./cmd/migration
	$(GO) build -ldflags="-s -w" -o ./bin/worker ./cmd/worker

wire:
	$(GO) generate ./cmd/server/wire ./cmd/migration/wire ./cmd/worker/wire

mock:
	$(GO) run github.com/golang/mock/mockgen@v1.6.0 -source=internal/service/user.go -destination test/mocks/service/user.go
	$(GO) run github.com/golang/mock/mockgen@v1.6.0 -source=internal/repository/user.go -destination test/mocks/repository/user.go
	$(GO) run github.com/golang/mock/mockgen@v1.6.0 -source=internal/repository/repository.go -destination test/mocks/repository/repository.go

swag:
	$(GO) run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/server/main.go -o ./docs
