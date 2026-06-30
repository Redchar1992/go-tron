VERSION ?= 0.0.0-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
PKG     := github.com/Redchar1992/go-tron/internal/version
LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).GitCommit=$(COMMIT)

.PHONY: build run test vet fmt fmtcheck tidy clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/gotron ./cmd/gotron

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/gotron

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmtcheck:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

tidy:
	go mod tidy

clean:
	rm -rf bin
