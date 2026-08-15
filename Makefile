.PHONY: all build clean run tidy

all: clean tidy build

build:
	mkdir -p dist
	go build -o dist/quickflare-go main.go

tidy:
	go mod tidy

run: build
	./dist/quickflare-go

clean:
	rm -rf dist
	rm -f cloudflared-windows-* cloudflared-linux-* cloudflared-darwin-*
