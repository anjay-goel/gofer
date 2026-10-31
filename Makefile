SOURCES := $(shell find . -name '*.go' -o -name '*.md' -o -name 'go.mod' | grep -v '^./dist' | sort)
VERSION ?= 0.1.0-$(shell cat $(SOURCES) | shasum | cut -c1-10)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := linux-amd64 linux-arm64
RELEASES := darwin-arm64 darwin-amd64 linux-amd64 linux-arm64

.PHONY: build install dist test clean payload

build: payload
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/gofer .
	rm -f internal/payload/bin/*.gz

install: build
	mkdir -p $(HOME)/.local/bin
	cp bin/gofer $(HOME)/.local/bin/gofer

payload:
	rm -f internal/payload/bin/*.gz
	for t in $(TARGETS); do \
		GOOS=$${t%-*} GOARCH=$${t#*-} CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o internal/payload/bin/gofer-$$t . ; \
		gzip -9 -f internal/payload/bin/gofer-$$t ; \
	done

dist: payload
	mkdir -p dist
	for t in $(RELEASES); do \
		GOOS=$${t%-*} GOARCH=$${t#*-} CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/gofer-$$t . ; \
	done
	rm -f internal/payload/bin/*.gz

test:
	go test ./...

clean:
	rm -rf bin dist internal/payload/bin/*.gz
