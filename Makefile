APP := newrrowllm

.PHONY: build test fmt tidy download install

build:
	go build -o bin/$(APP) .

download:
	go mod download

install:
	go install .

test:
	go test ./...

fmt:
	gofmt -w ./cmd ./internal ./main.go

tidy:
	go mod tidy
