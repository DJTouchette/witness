BIN := witness
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: build test e2e golden vet clean install

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/witness

test:
	go test ./... -count=1

# The end-to-end golden tests on their own, with the per-fixture output shown.
e2e:
	go test ./internal/e2e -count=1 -v

# Regenerate the golden files after an intentional behaviour change. Read the
# resulting diff: every line of it is a change to what witness tells CI.
golden:
	go test ./internal/e2e -count=1 -update

vet:
	go vet ./...

# Also drops recon's analysis cache: it lives in the working tree (.recon/ by
# default, .witness/ when --cache-dir pointed there) and a stale one changes
# what witness selects.
clean:
	rm -f $(BIN)
	rm -rf .recon .witness

install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/witness
