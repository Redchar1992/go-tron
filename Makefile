VERSION ?= 0.0.0-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
PKG     := github.com/Redchar1992/go-tron/internal/version
LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).GitCommit=$(COMMIT)

.PHONY: build run test vet fmt fmtcheck tidy clean oracle-ping oracle-corpus

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

oracle-ping:
	@printf '%s\n' '{"method":"ping"}' | tools/jtron-oracle/run.sh

oracle-corpus:
	@JTRON_ORACLE_INTEGRATION=1 go test -run 'TestJavaOracle(Corpus|RegressionVectors)' -count=1 ./internal/vmoracle
