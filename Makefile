.PHONY: test-e2e

test-e2e:
	go test ./internal/e2e/... -tags=e2e -timeout 10m
