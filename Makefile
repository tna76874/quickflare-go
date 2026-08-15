.PHONY: all build clean run tidy

all: clean tidy build

build:
	mkdir -p dist
	go build -o dist/quickflare-go main.go

build-cross:
	mkdir -p dist
	GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=$(GOARM) go build -o dist/quickflare-go-$(NAME) main.go

tidy:
	go mod tidy

run: build
	./dist/quickflare-go

clean:
	rm -rf dist
	rm -f cloudflared-windows-* cloudflared-linux-* cloudflared-darwin-*
