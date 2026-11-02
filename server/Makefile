VERSION ?= V15.4
NODE ?= node

.PHONY: build release frontend test vet run tidy

frontend:
	cd ../web-antd && $(NODE) scripts/build.mjs

build: frontend
	mkdir -p bin
	go build -ldflags "-X main.version=$(VERSION)" -o bin/proxypilot ./cmd/proxypilot

# 生产产物：静态 Linux 二进制，前端已 go:embed，服务器无需 Node/npm。
release: frontend
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" \
		-o bin/proxypilot-linux-amd64 ./cmd/proxypilot

test:
	go test ./...

vet:
	go vet ./...

run:
	go run ./cmd/proxypilot

tidy:
	go mod tidy
