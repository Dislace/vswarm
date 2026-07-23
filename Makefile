BINARY := vswarm

.PHONY: build vet fmt fmtcheck lint test-tooling clean

build:
	go build -o $(BINARY) ./cmd/vswarm

vet:
	go vet ./...

fmt:
	gofmt -w .

fmtcheck:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

lint:
	@if command -v shellcheck >/dev/null 2>&1; then shellcheck templates/entrypoint.sh.tmpl templates/vswarm-tooling.sh.tmpl scripts/*.sh; else echo "shellcheck not installed — skipping"; fi
	@if command -v hadolint >/dev/null 2>&1; then hadolint templates/Dockerfile.tmpl; else echo "hadolint not installed — skipping"; fi

test-tooling:
	./scripts/test-vswarm-tooling.sh

clean:
	rm -f $(BINARY)
	rm -rf generated
