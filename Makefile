# Pin the toolchain. go.mod's go line is 1.26.11; Go omits a toolchain
# directive that only repeats that version.
export GOTOOLCHAIN := go1.26.11

BINARY := bin/feldcored

.PHONY: all build web go run dev clean tidy

all: build

## build: build the front-end, embed it, compile the single binary
build: web go

## web: install deps (if needed) and build the front-end into web/dist
web:
	cd web && npm install --no-audit --no-fund && npm run build

## go: compile the daemon (expects web/dist to exist)
go:
	go build -o $(BINARY) ./cmd/feldcored

## run: build and run
run: build
	./$(BINARY)

## dev: run the daemon only (serves last-built UI); use `npm --prefix web run dev` alongside
dev:
	go run ./cmd/feldcored

## tidy: go mod tidy
tidy:
	go mod tidy

## clean: remove build outputs
clean:
	rm -rf bin web/dist/assets web/dist/index.html
