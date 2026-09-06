BINARY := vswarm

.PHONY: build vet fmt fmtcheck lint clean

build:
	go build -o $(BINARY) ./cmd/vswarm

vet:
	go vet ./...

fmt:
	gofmt -w .

fmtcheck:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

lint:
	@if command -v shellcheck >/dev/null 2>&1; then shellcheck templates/*.sh.tmpl scripts/*.sh; else echo "shellcheck not installed — skipping"; fi
	@if command -v hadolint >/dev/null 2>&1; then hadolint templates/Dockerfile.tmpl; else echo "hadolint not installed — skipping"; fi

clean:
	rm -f $(BINARY)
	rm -rf generated
