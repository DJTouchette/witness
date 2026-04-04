BIN := witness
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: build test vet clean install

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/witness

test:
	go test ./... -count=1

vet:
	go vet ./...

clean:
	rm -f $(BIN)

install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/witness
