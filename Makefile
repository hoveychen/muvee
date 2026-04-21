export PATH := $(PATH):/opt/homebrew/bin

.PHONY: build muveectl muveectl-binaries run dev tidy lint \
        docker-up docker-down docker-build \
        web-install web-dev web-build embed-web

# ─── Web ─────────────────────────────────────────────────────────────────────

web-install:
	cd web && npm install

web-build:
	cd web && npm run build

# Copy the Vite build output into the embed package so it gets baked into the binary.
embed-web: web-build
	rm -rf web/embed/dist
	cp -r web/dist web/embed/dist

# ─── Go binary ───────────────────────────────────────────────────────────────

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)

# Build the single muvee binary (embeds web first).
build: embed-web
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/muvee ./cmd/muvee

# Build the muveectl CLI binary.
# Copies the canonical skill file into the package so go:embed can pick it up.
muveectl:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/muveectl ./cmd/muveectl

# Cross-compile muveectl for all supported platforms and gzip the results into
# internal/muveectlbin/binaries/ so the next `make build` embeds them into the
# muvee server binary. Safe to skip for local dev — the server transparently
# falls back to the GitHub release when a requested asset isn't embedded.
muveectl-binaries:
	@mkdir -p internal/muveectlbin/binaries
	@for GOOS in linux darwin; do \
	  for GOARCH in amd64 arm64; do \
	    OUT=/tmp/muveectl_$${GOOS}_$${GOARCH}; \
	    echo "Building $$OUT"; \
	    CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH go build -ldflags="$(LDFLAGS)" -o $$OUT ./cmd/muveectl || exit 1; \
	    gzip -9 -c $$OUT > internal/muveectlbin/binaries/$$(basename $$OUT).gz; \
	    rm -f $$OUT; \
	  done; \
	done
	@for GOARCH in amd64 arm64; do \
	  OUT=/tmp/muveectl_windows_$${GOARCH}.exe; \
	  echo "Building $$OUT"; \
	  CGO_ENABLED=0 GOOS=windows GOARCH=$$GOARCH go build -ldflags="$(LDFLAGS)" -o $$OUT ./cmd/muveectl || exit 1; \
	  gzip -9 -c $$OUT > internal/muveectlbin/binaries/$$(basename $$OUT).gz; \
	  rm -f $$OUT; \
	done
	@ls -lh internal/muveectlbin/binaries/

# Quick server run without re-embedding (assumes dist is current).
run: build
	./bin/muvee server

# ─── Dev ─────────────────────────────────────────────────────────────────────

web-dev:
	cd web && npm run dev

# ─── Quality ─────────────────────────────────────────────────────────────────

tidy:
	go mod tidy

lint:
	go vet ./...

# ─── Docker ──────────────────────────────────────────────────────────────────

docker-network:
	docker network create muvee-net 2>/dev/null || true

docker-up: docker-network
	docker compose up -d

docker-down:
	docker compose down

# Build using Dockerfile.local (offline / from source).
docker-build:
	docker build -f Dockerfile.local -t muvee:local .
