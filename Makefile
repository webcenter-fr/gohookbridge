NAME  := gohookbridge
TARGET_URL ?= http://localhost:8080
SMEE_URL ?= https://smee.io/new
IMAGE_VERSION ?= latest
MD_FILES := $(shell find . -type f -regex ".*md"  -not -regex '^./vendor/.*' -not -regex '^./.vale/.*' -not -regex "^./.git/.*" -print)

LDFLAGS := -s -w
FLAGS += -ldflags "$(LDFLAGS)" -buildvcs=true
OUTPUT_DIR = bin
TEST_FLAGS = -v 
COVERAGE_FLAGS = -coverprofile=coverage.out -covermode=atomic 
all: test lint web-build build

FORCE:


$(OUTPUT_DIR)/$(NAME): FORCE
	go build $(FLAGS)  -o $@ ./cmd/gohookbridge

$(OUTPUT_DIR)/$(NAME)-aarch64-linux: FORCE
	env GOARCH=arm64 GOOS=linux	go build $(FLAGS)   -o $@ ./cmd/gohookbridge

$(OUTPUT_DIR)/gohookbridge-client: FORCE
	go build $(FLAGS)  -o $@ ./cmd/gohookbridge-client

$(OUTPUT_DIR)/gohookbridge-client-aarch64-linux: FORCE
	env GOARCH=arm64 GOOS=linux	go build $(FLAGS)   -o $@ ./cmd/gohookbridge-client

$(OUTPUT_DIR)/gohookbridge-proxy: FORCE
	go build $(FLAGS)  -o $@ ./cmd/gohookbridge-proxy

$(OUTPUT_DIR)/gohookbridge-proxy-aarch64-linux: FORCE
	env GOARCH=arm64 GOOS=linux	go build $(FLAGS)   -o $@ ./cmd/gohookbridge-proxy

.PHONY: web-build
web-build:
	cd web && npm ci && npm run build

test:
	@go test $(TEST_FLAGS) ./... 

.PHONY: html-coverage
html-coverage: ## generate html coverage
	@mkdir -p tmp
	@go test $(COVERAGE_FLAGS) -coverprofile=tmp/c.out ./.../ && go tool cover -html=tmp/c.out

clean:
	@rm -rf $(OUTPUT_DIR)/$(NAME) $(OUTPUT_DIR)/gohookbridge-client $(OUTPUT_DIR)/gohookbridge-proxy $(OUTPUT_DIR)/$(NAME)-aarch64-linux

build: web-build clean
	@echo "building."
	@mkdir -p $(OUTPUT_DIR)/
	@go build  $(FLAGS)  -o $(OUTPUT_DIR)/$(NAME) ./cmd/gohookbridge

build-client:
	@go build $(FLAGS) -o $(OUTPUT_DIR)/gohookbridge-client ./cmd/gohookbridge-client

build-proxy:
	@go build $(FLAGS) -o $(OUTPUT_DIR)/gohookbridge-proxy ./cmd/gohookbridge-proxy

build-all: build build-client build-proxy

lint: lint-go lint-md

lint-go:
	@echo "linting."
	golangci-lint version
	golangci-lint run ./...

.PHONY: lint-md
lint-md: ${MD_FILES} ## runs markdownlint and vale on all markdown files
	@echo "Linting markdown files..."
	@markdownlint $(MD_FILES)
	@echo "Grammar check with vale of documentation..."
	@vale docs/content --minAlertLevel=error --output=line

dev-server:
	reflex -r '.*\.(tmpl|go)' -s go run ./cmd/gohookbridge -- server --footer "Contact: <a href=\"https://twitter.com/me\">Me</a> - use it at your own risk"

fmt:
	@go fmt `go list ./...`

fumpt:
	@gofumpt -e -w -extra ./