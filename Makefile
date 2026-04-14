.PHONY: build test test-cover test-cover-check vet ci test-e2e

# Coverage floor enforced by CI (cb-60f26b). Ratchet up as tests land —
# do not drop. Current baseline: ~40 % at commit 7875e5e. Raising the
# floor requires a PR that adds tests; lowering it requires a root-cause
# explanation (deletion of covered code, etc.) in the commit message.
COVERAGE_FLOOR ?= 40.0

build:
	go build ./...

test:
	go test ./...

test-cover:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

test-cover-check: test-cover
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
	awk -v t="$$total" -v f="$(COVERAGE_FLOOR)" 'BEGIN { if (t+0 < f+0) { printf "FAIL: coverage %.1f%% < floor %.1f%% (see cb-60f26b in CLAUDE.md)\n", t, f; exit 1 } else { printf "OK: coverage %.1f%% >= floor %.1f%%\n", t, f } }'

vet:
	go vet ./...

ci: build test vet

test-e2e:
	go test ./internal/e2e/... -tags=e2e -timeout 10m
