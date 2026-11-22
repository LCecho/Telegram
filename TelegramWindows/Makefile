SHELL := /bin/sh
BIN_DIR := bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

export CGO_ENABLED := 0

.PHONY: all build cli test race vet fmt lint run selftest clean help

all: build

## build: 编译 GUI（bin/tgw.exe）
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/tgw.exe ./cmd/tgw

## cli: 编译无界面工具（bin/tgw-cli.exe）
cli:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/tgw-cli.exe ./cmd/tgw-cli

## test: 跑单元测试（默认不联网）
test:
	go test ./...

## race: 带竞态检测
race:
	go test -race ./...

## vet: go vet
vet:
	go vet ./...

## fmt: 检查格式（有输出即失败）
fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "未格式化文件："; echo "$$out"; exit 1; fi

## lint: 格式 + vet
lint: fmt vet

## run: 直接运行 GUI
run:
	go run ./cmd/tgw

## selftest: 跑本地自检
selftest: cli
	./$(BIN_DIR)/tgw-cli.exe selftest

## clean: 清理产物
clean:
	rm -rf $(BIN_DIR)

## help: 列出目标
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
