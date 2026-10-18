# List the recipes
default:
    @just --list

# Build ./agentpr
build:
    go build -o agentpr .

# Build into ~/.local/bin, which must be on PATH: Claude runs `agentpr reply`
install:
    go build -o ~/.local/bin/agentpr .

# Run from source, e.g. `just run --theme catppuccin-latte`
run *args:
    go run . {{args}}

test:
    go test -race ./...

vet:
    go vet ./...

fmt:
    gofmt -w .

# vet, plus staticcheck when it's installed
lint: vet
    @if command -v staticcheck >/dev/null; then staticcheck ./...; else echo "staticcheck not installed, skipped"; fi

# What CI runs: gofmt check, vet, race tests
check:
    @test -z "$(gofmt -l .)" || { echo "needs gofmt:"; gofmt -l .; exit 1; }
    go vet ./...
    go test -race ./...

# Race tests with coverage; prints the total
cover:
    go test -race -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1

# Coverage by line, in the browser
cover-html: cover
    go tool cover -html=coverage.out

# Regenerate docs/images (needs tmux and ImageMagick)
screenshots:
    docs/screenshots.sh

clean:
    rm -f agentpr coverage.out
