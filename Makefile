BIN     := oss
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)

# 发布目标平台: GOOS/GOARCH
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build install test tidy fmt clean release

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

install:
	go install -trimpath -ldflags "$(LDFLAGS)" .

test:
	go test ./...

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

clean:
	rm -rf $(BIN) dist

# 本地交叉编译发布产物（CI 中另做 UPX 压缩与打包，见 .github/workflows/release.yml）
release:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=dist/$(BIN)-$$os-$$arch; \
		[ "$$os" = "windows" ] && out=$$out.exe; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -trimpath -ldflags "$(LDFLAGS)" -o $$out . || exit 1; \
	done
	@ls -lh dist/
