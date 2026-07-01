# shoal — a terminal BitTorrent client in Go
#
# Common tasks. Run `make help` for the list.

BINARY      := shoal
PKG         := ./...
CMD_TUI     := ./cmd/shoal
CMD_CLASSIC := ./cmd/shoal-classic

.DEFAULT_GOAL := help

## help: print this help
.PHONY: help
help:
	@echo "shoal — make targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

## deps: fetch and pin all dependencies (run this once after cloning)
.PHONY: deps
deps:
	go get github.com/charmbracelet/bubbletea@latest
	go get github.com/charmbracelet/bubbles@latest
	go get github.com/charmbracelet/lipgloss@latest
	go get github.com/anacrolix/torrent@latest
	go mod tidy

## build: build the TUI binary into ./shoal
.PHONY: build
build:
	go build -o $(BINARY) $(CMD_TUI)

## install: install the shoal binary into GOBIN (go install ./cmd/shoal)
.PHONY: install
install:
	go install $(CMD_TUI)

## classic: build the phase-1 hand-written CLI downloader
.PHONY: classic
classic:
	go build -o $(BINARY)-classic $(CMD_CLASSIC)

## run: build and launch the TUI
.PHONY: run
run: build
	./$(BINARY)

## test: run all unit tests
.PHONY: test
test:
	go test $(PKG)

## vet: run go vet
.PHONY: vet
vet:
	go vet $(PKG)

## fmt: format all Go source
.PHONY: fmt
fmt:
	gofmt -w .

## fmt-check: fail if any file is not gofmt-clean (used by CI)
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi

## tidy: sync go.mod / go.sum
.PHONY: tidy
tidy:
	go mod tidy

## clean: remove build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY) $(BINARY)-classic
	rm -rf downloads/
