.PHONY: build test vet ci test-e2e

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

ci: build test vet

test-e2e:
	go test ./internal/e2e/... -tags=e2e -timeout 10m
