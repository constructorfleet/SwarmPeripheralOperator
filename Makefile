GO        ?= go
PKG       := github.com/constructorfleet/SwarmPeripheralOperator
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS   := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)
BIN       ?= bin
GOOS      ?= linux
GOARCH    ?= $(shell $(GO) env GOARCH)
CMDS      := $(notdir $(wildcard cmd/*))

.PHONY: all test integration lint fmt fmt-check vet vuln secrets build check clean

all: check

test:
	$(GO) test -race -count=1 ./...

# Needs Docker with privileged containers. SPO_DIND_IMAGE selects the Engine under test.
integration:
	$(GO) test -tags integration -count=1 -timeout 30m -v ./tests/integration/

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .

fmt-check:
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

secrets:
	gitleaks git --no-banner --redact .

build:
	@mkdir -p $(BIN)
	@for c in $(CMDS); do \
		echo "build $$c ($(GOOS)/$(GOARCH))"; \
		CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/$$c ./cmd/$$c || exit 1; \
	done

check: fmt-check vet lint test vuln

clean:
	rm -rf $(BIN)
