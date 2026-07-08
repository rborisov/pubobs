.PHONY: build frontend build-amd64 build-arm64 clean

GOFLAGS = CGO_ENABLED=0
# The backend has no tagged releases of its own (unlike the Obsidian
# plugin's independent semver), so the in-app self-updater treats the git
# commit SHA as the build's "version" — Check() compares this verbatim
# against the latest commit SHA `git ls-remote` reports for the configured
# update branch, so this must be the FULL SHA (not --short); the update UI
# truncates it for display via shortVersion().
VERSION ?= $(shell git rev-parse HEAD 2>/dev/null || echo dev)
LDFLAGS = -ldflags="-s -w -X github.com/pubobs/backend/internal/api.Version=$(VERSION)"

# The frontend bundle is go:embed'd into the binary at compile time (see
# frontend/embed.go), and the Docker image just copies these prebuilt Linux
# binaries rather than building from source — so a binary rebuilt without
# first refreshing frontend/static/app.js silently ships stale JS, and
# `install.sh --update` will look like it did nothing. Building frontend as
# part of `build` (rather than relying on remembering to run `npm run build`
# first) makes that mistake impossible.
build: frontend build-amd64 build-arm64

frontend:
	cd ../frontend && npm run build

build-amd64:
	$(GOFLAGS) GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/pubobs-linux-amd64 ./cmd/server

build-arm64:
	$(GOFLAGS) GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/pubobs-linux-arm64 ./cmd/server

clean:
	rm -f bin/pubobs-linux-amd64 bin/pubobs-linux-arm64
