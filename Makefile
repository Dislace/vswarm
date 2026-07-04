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
	@command -v shellcheck >/dev/null 2>&1 && shellcheck templates/entrypoint.sh.tmpl || echo "shellcheck not installed — skipping"
	@command -v hadolint  >/dev/null 2>&1 && hadolint templates/Dockerfile.tmpl      || echo "hadolint not installed — skipping"

clean:
	rm -f $(BINARY)
	rm -rf generated
