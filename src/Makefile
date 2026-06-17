APP := epusdt
TARGET_DIR := bin
BUILD_TAG ?= 0.0.0-dev
VERSION_PKG := github.com/GMWalletApp/epusdt/config
LDFLAGS := -s -w -X $(VERSION_PKG).BuildVersion=$(BUILD_TAG)

.PHONY: build release build-all swagger \
	linux-amd64 linux-arm64 linux-armv7 \
	darwin-amd64 darwin-arm64 \
	windows-amd64 windows-arm64

build:
	go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP) .

release: build

linux-amd64:
	GOOS=linux GOARCH=amd64 go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP)-linux-amd64 .

linux-arm64:
	GOOS=linux GOARCH=arm64 go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP)-linux-arm64 .

linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP)-linux-armv7 .

darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP)-darwin-amd64 .

darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP)-darwin-arm64 .

windows-amd64:
	GOOS=windows GOARCH=amd64 go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP)-windows-amd64.exe .

windows-arm64:
	GOOS=windows GOARCH=arm64 go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP)-windows-arm64.exe .

build-all: linux-amd64 linux-arm64 linux-armv7 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64

swagger:
	swag init --outputTypes json,yaml --parseInternal --parseDependency -o docs
