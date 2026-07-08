.PHONY: build frontend build-amd64 build-arm64 clean check-version

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

# check-version catches the exact bug that shipped commit 7baf844a with a
# binary whose embedded api.Version string named the *parent* commit
# (93568ce), not itself: VERSION above is computed from `git rev-parse HEAD`
# at build time, which is necessarily the commit BEFORE whichever one the
# freshly-built binary ends up committed into — a binary can never embed the
# hash of a commit it's part of (the commit hash is derived from the tree,
# which includes the binary's own bytes). So this must be run AFTER `git
# commit`, not as part of `build`, and it only passes when source and
# binaries were committed as two separate commits in the right order (source
# first, then a follow-up "build: rebuild deployed binaries" commit built
# from that now-immutable HEAD) — see README's Development section.
check-version:
	@head=$$(git rev-parse HEAD); \
	status=0; \
	for arch in amd64 arm64; do \
		bin="bin/pubobs-linux-$$arch"; \
		[ -f "$$bin" ] || continue; \
		embedded=$$(strings "$$bin" | grep -E '^[0-9a-f]{40}$$' | sort -u); \
		count=$$(printf '%s\n' "$$embedded" | grep -c .); \
		if [ "$$count" -ne 1 ]; then \
			echo "check-version: FAIL $$bin — expected exactly one embedded 40-char version SHA, found $$count: $$embedded" >&2; \
			status=1; continue; \
		fi; \
		if [ "$$embedded" != "$$head" ]; then \
			echo "check-version: FAIL $$bin embeds $$embedded but HEAD is $$head" >&2; \
			echo "  -> rebuild AFTER committing source (make build), then commit the binaries" >&2; \
			echo "     in their own follow-up commit; do not amend source+binary together." >&2; \
			status=1; continue; \
		fi; \
		echo "check-version: OK $$bin ($$embedded)"; \
	done; \
	exit $$status
