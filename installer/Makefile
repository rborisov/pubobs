.PHONY: build build-amd64 build-arm64 clean

GOFLAGS = CGO_ENABLED=0
LDFLAGS = -ldflags="-s -w"

build: build-amd64 build-arm64

build-amd64:
	$(GOFLAGS) GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/installer-linux-amd64 .

build-arm64:
	$(GOFLAGS) GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/installer-linux-arm64 .

clean:
	rm -f bin/installer-linux-amd64 bin/installer-linux-arm64
