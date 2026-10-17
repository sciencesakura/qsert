.PHONY: all build generate test lint clean

all: generate build

build:
	go build -o qsert ./cmd/qsert

generate:
	go generate ./...

test:
	go test ./...

lint:
	golangci-lint run --fix ./...

clean:
	rm -f qsert
