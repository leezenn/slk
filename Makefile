VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/leezenn/slk/cmd.version=$(VERSION)

.PHONY: build install clean release

build:
	go build -ldflags "$(LDFLAGS)" -o slk .

install: build
	cp slk ~/.local/bin/slk

clean:
	rm -f slk dist/*

release: clean
	@mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/slk-darwin-arm64 .
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/slk-linux-amd64 .
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/slk-windows-amd64.exe .
	@echo "Built $(VERSION):"
	@ls -lh dist/
