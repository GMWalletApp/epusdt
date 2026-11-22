APP := epusdt
TARGET_DIR := bin
BUILD_TAG ?= 0.0.0-dev
VERSION_PKG := github.com/GMWalletApp/epusdt/config
LDFLAGS := -s -w -X $(VERSION_PKG).BuildVersion=$(BUILD_TAG) -X $(VERSION_PKG).BuildType=source

.PHONY: build release build-all swagger \
	linux-amd64 linux-arm64 linux-armv7 \
	darwin-amd64 darwin-arm64 \
	windows-amd64 windows-arm64

build:
	go build --trimpath -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/$(APP) .

release:
	@if ! printf '%s\n' "$(BUILD_TAG)" | grep -Eq '^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$$'; then \
		echo "BUILD_TAG must be a stable semantic version such as v2.1.0" >&2; \
		exit 1; \
	fi
	go build --trimpath -ldflags="-s -w -X $(VERSION_PKG).BuildVersion=$(BUILD_TAG) -X $(VERSION_PKG).BuildType=release" -o $(TARGET_DIR)/$(APP) .

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
	swag init --outputTypes json,yaml --parseInternal --parseDependency --parseFuncBody -o docs
