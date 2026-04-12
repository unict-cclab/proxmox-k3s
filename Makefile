BINARY   := proxmox-k3s
CMD      := ./cmd/main.go
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: build test lint clean

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) $(CMD)

test:
	go test ./...

lint:
	golangci-lint run

clean:
	rm -f $(BINARY)
