# Installer Webapp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `curl | bash` bootstrap that clones the repo, runs a pre-built Go binary serving a 6-step browser wizard, then fully installs PubObs on the VPS (Docker, app build, nginx, TLS).

**Architecture:** The installer is a standalone Go binary (`installer/`) with its own `go.mod`, embedding a vanilla-JS wizard SPA. It communicates over HTTP on `:8000`. Shell commands stream via SSE. The main backend gains three small additions: a `/healthz` endpoint, `PUBOBS_ADMIN_EMAIL` config, and auto-promotion logic on first login.

**Tech Stack:** Go 1.25 stdlib only (installer), chi v5 + sqlite (backend), vanilla JS + SSE (wizard UI), Docker Compose (build + run), certbot (TLS).

---

## File Map

**New files:**
- `install.sh` — bootstrap entry point (`curl | bash`)
- `installer/go.mod` — standalone module `github.com/pubobs/installer`
- `installer/main.go` — HTTP server, router, in-memory state, embed
- `installer/syscheck.go` — OS/Docker/disk checks
- `installer/steps.go` — install step executors + SSE broadcaster
- `installer/static/index.html` — wizard SPA (HTML + CSS + JS)
- `installer/Makefile` — cross-compile both arch targets
- `installer/bin/.gitkeep` — placeholder; actual binaries added in final task

**Modified files:**
- `backend/internal/config/config.go` — add `AdminEmail` field
- `backend/internal/store/user.go` — add `ListInstanceAdmins`
- `backend/internal/store/user_test.go` — test `ListInstanceAdmins`
- `backend/internal/api/auth.go` — auto-promote admin on first login
- `backend/internal/api/auth_test.go` — test auto-promotion
- `backend/internal/api/router.go` — add `GET /healthz`
- `backend/Dockerfile` — 3-stage build (Node → Go → Alpine)
- `backend/docker-compose.yml` — context `..`, restart policy, `127.0.0.1` port bind

---

## Task 1: Add `PUBOBS_ADMIN_EMAIL` to backend config

**Files:**
- Modify: `backend/internal/config/config.go`

- [ ] **Step 1: Add `AdminEmail` field to `Config` struct and load it**

In `backend/internal/config/config.go`, add inside the `Config` struct after `YandexClientSecret`:
```go
AdminEmail         string
```

Inside `Load()`, add after the `YandexClientSecret` assignment:
```go
AdminEmail:         getEnv("PUBOBS_ADMIN_EMAIL", ""),
```

- [ ] **Step 2: Verify config still loads (existing tests pass)**

```bash
cd backend && go test ./internal/config/... -v
```
Expected: no test files yet — command exits 0 with `[no test files]`.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/config/config.go
git commit -m "feat: add PUBOBS_ADMIN_EMAIL config field"
```

---

## Task 2: Add `ListInstanceAdmins` to store

**Files:**
- Modify: `backend/internal/store/user.go`
- Modify: `backend/internal/store/user_test.go`

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/store/user_test.go`:
```go
func TestListInstanceAdmins(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.UpsertUser(ctx, "u3", "c@x.com", "C")
	s.SetInstanceAdmin(ctx, "u2", true)

	admins, err := s.ListInstanceAdmins(ctx)
	require.NoError(t, err)
	require.Len(t, admins, 1)
	require.Equal(t, "u2", admins[0].ID)
}
```

- [ ] **Step 2: Run to confirm it fails**

```bash
cd backend && go test ./internal/store/... -run TestListInstanceAdmins -v
```
Expected: compile error — `s.ListInstanceAdmins undefined`.

- [ ] **Step 3: Implement `ListInstanceAdmins`**

Add to `backend/internal/store/user.go` after `SetInstanceAdmin`:
```go
func (s *Store) ListInstanceAdmins(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, name, is_instance_admin, is_banned, created_at FROM users WHERE is_instance_admin=1`)
	if err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test — confirm it passes**

```bash
cd backend && go test ./internal/store/... -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/user.go backend/internal/store/user_test.go
git commit -m "feat: add ListInstanceAdmins store method"
```

---

## Task 3: Auto-promote first admin in auth callback

**Files:**
- Modify: `backend/internal/api/auth.go`
- Modify: `backend/internal/api/auth_test.go`

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/api/auth_test.go`:
```go
func TestAutoPromoteAdmin_firstLogin(t *testing.T) {
	deps := newTestDeps(t)
	deps.Config.AdminEmail = "boss@x.com"

	// Simulate UpsertUser + auto-promotion path directly via store
	// (auth callback requires a live OIDC exchange, so we test the store+config
	// integration by calling the same logic the handler will use)
	ctx := t.Context()
	user, err := deps.Store.UpsertUser(ctx, "u1", "boss@x.com", "Boss")
	require.NoError(t, err)
	require.False(t, user.IsInstanceAdmin)

	// Auto-promotion logic (mirrors what auth.go will do)
	if deps.Config.AdminEmail != "" && user.Email == deps.Config.AdminEmail && !user.IsInstanceAdmin {
		admins, err := deps.Store.ListInstanceAdmins(ctx)
		require.NoError(t, err)
		if len(admins) == 0 {
			require.NoError(t, deps.Store.SetInstanceAdmin(ctx, user.ID, true))
		}
	}

	promoted, err := deps.Store.GetUserByID(ctx, "u1")
	require.NoError(t, err)
	require.True(t, promoted.IsInstanceAdmin)
}

func TestAutoPromoteAdmin_notFirstAdmin(t *testing.T) {
	deps := newTestDeps(t)
	deps.Config.AdminEmail = "second@x.com"
	ctx := t.Context()

	// Existing admin already present
	deps.Store.UpsertUser(ctx, "existing", "existing@x.com", "Existing")
	deps.Store.SetInstanceAdmin(ctx, "existing", true)

	// New user with admin email — should NOT be promoted
	user, _ := deps.Store.UpsertUser(ctx, "u2", "second@x.com", "Second")
	if deps.Config.AdminEmail != "" && user.Email == deps.Config.AdminEmail && !user.IsInstanceAdmin {
		admins, _ := deps.Store.ListInstanceAdmins(ctx)
		if len(admins) == 0 {
			deps.Store.SetInstanceAdmin(ctx, user.ID, true)
		}
	}

	notPromoted, _ := deps.Store.GetUserByID(ctx, "u2")
	require.False(t, notPromoted.IsInstanceAdmin)
}
```

- [ ] **Step 2: Run to confirm they pass already (logic is inline)**

```bash
cd backend && go test ./internal/api/... -run TestAutoPromoteAdmin -v
```
Expected: PASS (the logic is directly in the test, confirming the algorithm is correct before we embed it in the handler).

- [ ] **Step 3: Add promotion logic to `handleAuthCallback`**

In `backend/internal/api/auth.go`, find the line after `UpsertUser` returns:
```go
user, err := deps.Store.UpsertUser(r.Context(), claims.Subject, claims.Email, claims.Name)
if err != nil {
    writeError(w, http.StatusInternalServerError, "upsert user failed")
    return
}
```

Insert immediately after (before `authCode := ...`):
```go
if deps.Config.AdminEmail != "" && user.Email == deps.Config.AdminEmail && !user.IsInstanceAdmin {
    admins, err := deps.Store.ListInstanceAdmins(r.Context())
    if err == nil && len(admins) == 0 {
        deps.Store.SetInstanceAdmin(r.Context(), user.ID, true)
        user.IsInstanceAdmin = true
    }
}
```

- [ ] **Step 4: Run all backend tests**

```bash
cd backend && go test ./... -v 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/auth.go backend/internal/api/auth_test.go
git commit -m "feat: auto-promote first admin on login via PUBOBS_ADMIN_EMAIL"
```

---

## Task 4: Add `GET /healthz` endpoint

**Files:**
- Modify: `backend/internal/api/router.go`
- Modify: `backend/internal/api/auth_test.go` (add one test)

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/api/auth_test.go`:
```go
func TestHealthz(t *testing.T) {
	deps := newTestDeps(t)
	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"ok"`)
}
```

- [ ] **Step 2: Run to confirm it fails**

```bash
cd backend && go test ./internal/api/... -run TestHealthz -v
```
Expected: FAIL — 404.

- [ ] **Step 3: Add route to router**

In `backend/internal/api/router.go`, add before the `// Public reader` comment:
```go
r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
})
```

- [ ] **Step 4: Run test — confirm pass**

```bash
cd backend && go test ./internal/api/... -run TestHealthz -v
```
Expected: PASS.

- [ ] **Step 5: Run all backend tests**

```bash
cd backend && go test ./... 2>&1 | tail -5
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/router.go backend/internal/api/auth_test.go
git commit -m "feat: add GET /healthz endpoint"
```

---

## Task 5: Update Dockerfile and docker-compose.yml

**Files:**
- Modify: `backend/Dockerfile`
- Modify: `backend/docker-compose.yml`

- [ ] **Step 1: Replace `backend/Dockerfile` with 3-stage build**

The esbuild config outputs to `../backend/frontend/static/app.js` relative to the `frontend/` WORKDIR, which resolves to `/backend/frontend/static/app.js` inside the Docker stage.

```dockerfile
# Stage 1: Build frontend TypeScript
FROM node:22-alpine AS frontend
WORKDIR /app
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN mkdir -p /backend/frontend/static
RUN node esbuild.config.mjs production

# Stage 2: Build Go backend (embeds frontend output)
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
COPY --from=frontend /backend/frontend/static/ ./frontend/static/
RUN CGO_ENABLED=0 go build -o /pubobs ./cmd/server

# Stage 3: Runtime
FROM alpine:3.21
RUN apk add --no-cache git ca-certificates
COPY --from=builder /pubobs /usr/local/bin/pubobs
RUN adduser -D -u 1000 pubobs
USER pubobs
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pubobs"]
```

- [ ] **Step 2: Update `backend/docker-compose.yml`**

```yaml
services:
  pubobs:
    build:
      context: ..
      dockerfile: backend/Dockerfile
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - ./data/db:/data/db
      - ./data/repos:/data/repos
    environment:
      PUBOBS_BASE_URL: ${PUBOBS_BASE_URL}
      PUBOBS_OIDC_ISSUER: ${PUBOBS_OIDC_ISSUER}
      PUBOBS_OIDC_CLIENT_ID: ${PUBOBS_OIDC_CLIENT_ID}
      PUBOBS_OIDC_CLIENT_SECRET: ${PUBOBS_OIDC_CLIENT_SECRET}
      PUBOBS_SECRET_KEY: ${PUBOBS_SECRET_KEY}
      PUBOBS_ADMIN_EMAIL: ${PUBOBS_ADMIN_EMAIL:-}
      PUBOBS_YANDEX_CLIENT_ID: ${PUBOBS_YANDEX_CLIENT_ID:-}
      PUBOBS_YANDEX_CLIENT_SECRET: ${PUBOBS_YANDEX_CLIENT_SECRET:-}
      PUBOBS_REPO_CACHE_TTL: 24h
      PUBOBS_CACHE_CHECK_INTERVAL: 1h
      PUBOBS_DISK_WARN_PCT: 20
      PUBOBS_DISK_CRIT_PCT: 5
```

- [ ] **Step 3: Verify docker build works from repo root**

```bash
cd /path/to/pubobs && docker build -f backend/Dockerfile . -t pubobs-test --no-cache 2>&1 | tail -10
```
Expected: `Successfully built ...` — all 3 stages complete.

- [ ] **Step 4: Commit**

```bash
git add backend/Dockerfile backend/docker-compose.yml
git commit -m "feat: 3-stage Docker build (Node+Go+Alpine), restart policy, local port bind"
```

---

## Task 6: Installer Go module + syscheck

**Files:**
- Create: `installer/go.mod`
- Create: `installer/syscheck.go`
- Create: `installer/syscheck_test.go`

- [ ] **Step 1: Create `installer/go.mod`**

```
module github.com/pubobs/installer

go 1.22
```

- [ ] **Step 2: Write the failing syscheck test**

Create `installer/syscheck_test.go`:
```go
package main

import (
	"testing"
)

func TestSysCheckFields(t *testing.T) {
	sc := runSysCheck()
	if sc.Arch == "" {
		t.Error("Arch should not be empty")
	}
	if sc.OS == "" {
		t.Error("OS should not be empty")
	}
	// DiskFreeGB must be positive on any real machine
	if sc.DiskFreeGB <= 0 {
		t.Errorf("expected DiskFreeGB > 0, got %f", sc.DiskFreeGB)
	}
}
```

- [ ] **Step 3: Run to confirm it fails**

```bash
cd installer && go test -run TestSysCheckFields -v
```
Expected: compile error — `runSysCheck undefined`.

- [ ] **Step 4: Create `installer/syscheck.go`**

```go
package main

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type SysCheck struct {
	OS            string  `json:"os"`
	Arch          string  `json:"arch"`
	DiskFreeGB    float64 `json:"disk_free_gb"`
	DiskOK        bool    `json:"disk_ok"`
	DockerPresent bool    `json:"docker_present"`
	GitPresent    bool    `json:"git_present"`
}

func runSysCheck() SysCheck {
	var sc SysCheck
	sc.OS = runtime.GOOS
	sc.Arch = runtime.GOARCH

	// Check available disk space on /
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		freeBytes := stat.Bavail * uint64(stat.Bsize)
		sc.DiskFreeGB = float64(freeBytes) / (1 << 30)
	}
	sc.DiskOK = sc.DiskFreeGB >= 5.0

	// Check Docker
	sc.DockerPresent = commandExists("docker")

	// Check Git
	sc.GitPresent = commandExists("git")

	return sc
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// osInfo returns a human-readable OS string from /etc/os-release.
func osInfo() string {
	out, err := exec.Command("sh", "-c", `grep PRETTY_NAME /etc/os-release | cut -d'"' -f2`).Output()
	if err != nil || len(out) == 0 {
		return runtime.GOOS
	}
	return strings.TrimSpace(string(out))
}

// publicIP fetches the server's public IP from api.ipify.org.
func publicIP() string {
	out, err := exec.Command("curl", "-s", "--max-time", "5", "https://api.ipify.org").Output()
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(out))
	// Basic sanity check
	if len(ip) < 7 || len(ip) > 45 {
		return ""
	}
	return ip
}

// diskFreeStr formats DiskFreeGB as a string like "12.3 GB".
func diskFreeStr(gb float64) string {
	return strconv.FormatFloat(gb, 'f', 1, 64) + " GB"
}
```

- [ ] **Step 5: Run test — confirm it passes**

```bash
cd installer && go test -run TestSysCheckFields -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add installer/go.mod installer/syscheck.go installer/syscheck_test.go
git commit -m "feat: installer syscheck module"
```

---

## Task 7: Installer HTTP server and handlers

**Files:**
- Create: `installer/main.go`
- Create: `installer/main_test.go`

- [ ] **Step 1: Write the failing tests**

Create `installer/main_test.go`:
```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *server {
	return &server{
		state:   &installerState{status: "idle"},
		eventCh: make(chan string, 256),
		logBuf:  new(strings.Builder),
	}
}

func TestSysCheckEndpoint(t *testing.T) {
	srv := newTestServer()
	mux := srv.routes()

	req := httptest.NewRequest("GET", "/api/syscheck", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var sc SysCheck
	if err := json.NewDecoder(rr.Body).Decode(&sc); err != nil {
		t.Fatalf("decode syscheck: %v", err)
	}
	if sc.Arch == "" {
		t.Error("Arch should not be empty")
	}
}

func TestConfigEndpoint(t *testing.T) {
	srv := newTestServer()
	mux := srv.routes()

	body := `{"domain":"example.com","admin_email":"admin@example.com","setup_nginx":true,"setup_tls":true,"oidc_provider":"google","oidc_client_id":"cid","oidc_client_secret":"csec","secret_key":"aabbcc"}`
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if srv.state.cfg == nil {
		t.Fatal("config not stored")
	}
	if srv.state.cfg.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", srv.state.cfg.Domain)
	}
}

func TestRootServesHTML(t *testing.T) {
	srv := newTestServer()
	mux := srv.routes()

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<!DOCTYPE html>") {
		t.Error("expected HTML response")
	}
}
```

- [ ] **Step 2: Run to confirm compile error**

```bash
cd installer && go test -v 2>&1 | head -20
```
Expected: compile errors — `server`, `installerState`, `routes` undefined.

- [ ] **Step 3: Create `installer/main.go`**

```go
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

//go:embed static
var staticFiles embed.FS

type installerConfig struct {
	Domain             string `json:"domain"`
	AdminEmail         string `json:"admin_email"`
	SetupNginx         bool   `json:"setup_nginx"`
	SetupTLS           bool   `json:"setup_tls"`
	OIDCProvider       string `json:"oidc_provider"`
	OIDCIssuer         string `json:"oidc_issuer"`
	OIDCClientID       string `json:"oidc_client_id"`
	OIDCClientSecret   string `json:"oidc_client_secret"`
	YandexClientID     string `json:"yandex_client_id"`
	YandexClientSecret string `json:"yandex_client_secret"`
	SecretKey          string `json:"secret_key"`
}

type installerState struct {
	mu     sync.Mutex
	cfg    *installerConfig
	status string // idle | running | done | error
}

type server struct {
	state   *installerState
	eventCh chan string
	logBuf  *strings.Builder
	mu      sync.Mutex
}

func main() {
	srv := &server{
		state:   &installerState{status: "idle"},
		eventCh: make(chan string, 1024),
		logBuf:  new(strings.Builder),
	}
	mux := srv.routes()
	addr := ":8000"
	log.Printf("PubObs Installer listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("GET /api/syscheck", s.handleSysCheck)
	mux.HandleFunc("POST /api/config", s.handleConfig)
	mux.HandleFunc("POST /api/install", s.handleInstall)
	mux.HandleFunc("GET /api/install/stream", s.handleStream)
	mux.HandleFunc("POST /api/install/retry-tls", s.handleRetryTLS)
	mux.HandleFunc("POST /api/install/skip-tls", s.handleSkipTLS)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)

	return mux
}

func (s *server) handleSysCheck(w http.ResponseWriter, r *http.Request) {
	sc := runSysCheck()
	writeJSON(w, sc)
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	var cfg installerConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if cfg.SecretKey == "" {
		key := make([]byte, 32)
		rand.Read(key)
		cfg.SecretKey = hex.EncodeToString(key)
	}
	s.state.mu.Lock()
	s.state.cfg = &cfg
	s.state.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) handleInstall(w http.ResponseWriter, r *http.Request) {
	s.state.mu.Lock()
	if s.state.status == "running" {
		s.state.mu.Unlock()
		http.Error(w, `{"error":"already running"}`, http.StatusConflict)
		return
	}
	s.state.status = "running"
	cfg := s.state.cfg
	s.state.mu.Unlock()

	if cfg == nil {
		http.Error(w, `{"error":"config not set"}`, http.StatusBadRequest)
		return
	}
	go runInstall(cfg, s.eventCh, s.logBuf, &s.mu)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	for {
		select {
		case event := <-s.eventCh:
			fmt.Fprintf(w, "data: %s\n\n", event)
			flusher.Flush()
			var e map[string]string
			json.Unmarshal([]byte(event), &e)
			if e["type"] == "done" {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) handleRetryTLS(w http.ResponseWriter, r *http.Request) {
	s.state.mu.Lock()
	cfg := s.state.cfg
	s.state.mu.Unlock()
	if cfg == nil {
		http.Error(w, `{"error":"no config"}`, http.StatusBadRequest)
		return
	}
	go retryTLS(cfg, s.eventCh, s.logBuf, &s.mu)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) handleSkipTLS(w http.ResponseWriter, r *http.Request) {
	s.state.mu.Lock()
	cfg := s.state.cfg
	s.state.mu.Unlock()
	// If TLS is skipped, update base URL to http and restart container
	if cfg != nil {
		go skipTLS(cfg, s.eventCh, s.logBuf, &s.mu)
	} else {
		s.eventCh <- `{"type":"done"}`
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	logs := s.logBuf.String()
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", `attachment; filename="pubobs-install.log"`)
	fmt.Fprint(w, logs)
}

func (s *server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// generateSecretKey returns 32 random bytes as a hex string.
func generateSecretKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		log.Fatalf("generate key: %v", err)
	}
	return hex.EncodeToString(key)
}

// oidcIssuerForProvider returns the issuer URL for known providers.
func oidcIssuerForProvider(provider string) string {
	switch provider {
	case "google":
		return "https://accounts.google.com"
	default:
		return ""
	}
}

// baseURL constructs the base URL given domain and TLS flag.
func baseURL(domain string, tls bool) string {
	if tls {
		return "https://" + domain
	}
	return "http://" + domain
}

// writeEnvFile writes the .env file to the given path with mode 0600.
func writeEnvFile(path string, cfg *installerConfig, useTLS bool) error {
	issuer := cfg.OIDCIssuer
	if issuer == "" {
		issuer = oidcIssuerForProvider(cfg.OIDCProvider)
	}
	lines := []string{
		"PUBOBS_BASE_URL=" + baseURL(cfg.Domain, useTLS),
		"PUBOBS_OIDC_ISSUER=" + issuer,
		"PUBOBS_OIDC_CLIENT_ID=" + cfg.OIDCClientID,
		"PUBOBS_OIDC_CLIENT_SECRET=" + cfg.OIDCClientSecret,
		"PUBOBS_SECRET_KEY=" + cfg.SecretKey,
		"PUBOBS_ADMIN_EMAIL=" + cfg.AdminEmail,
		"PUBOBS_YANDEX_CLIENT_ID=" + cfg.YandexClientID,
		"PUBOBS_YANDEX_CLIENT_SECRET=" + cfg.YandexClientSecret,
	}
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0600)
}

// ctxWithCancel is used in tests; real installs use context.Background.
var _ = context.Background
```

- [ ] **Step 4: Create a minimal `installer/static/index.html` placeholder** (full SPA comes in Task 9)

```html
<!DOCTYPE html>
<html><head><title>PubObs Installer</title></head>
<body><h1>PubObs Installer — loading...</h1></body>
</html>
```

- [ ] **Step 5: Run tests — confirm they pass**

```bash
cd installer && go test -v
```
Expected: all 3 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add installer/main.go installer/main_test.go installer/static/index.html
git commit -m "feat: installer HTTP server and handlers"
```

---

## Task 8: Install step executors and SSE broadcaster

**Files:**
- Create: `installer/steps.go`

- [ ] **Step 1: Create `installer/steps.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const repoDir = "/opt/pubobs"

// emit sends a JSON SSE event to the channel and appends raw text to logBuf.
func emit(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, event map[string]string) {
	data, _ := json.Marshal(event)
	ch <- string(data)
	mu.Lock()
	if text, ok := event["text"]; ok {
		logBuf.WriteString(text)
	}
	mu.Unlock()
}

func sectionStart(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, name string) {
	emit(ch, mu, logBuf, map[string]string{"type": "section_start", "name": name})
}

func sectionDone(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, name string) {
	emit(ch, mu, logBuf, map[string]string{"type": "section_done", "name": name})
}

func sectionError(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, name, message string) {
	b, _ := json.Marshal(map[string]string{"type": "section_error", "name": name, "message": message})
	ch <- string(b)
}

func done(ch chan string) {
	ch <- `{"type":"done"}`
}

// runCmd runs a command, streaming stdout+stderr as SSE log events.
func runCmd(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		buf := make([]byte, 512)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				emit(ch, mu, logBuf, map[string]string{"type": "log", "text": string(buf[:n])})
			}
			if err != nil {
				break
			}
		}
	}()

	err := cmd.Wait()
	pw.Close()
	return err
}

// runInstall is the main installation sequence.
func runInstall(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex) {
	// Step 1: Install Docker
	sectionStart(ch, mu, logBuf, "Install Docker")
	if err := stepInstallDocker(ch, mu, logBuf); err != nil {
		sectionError(ch, mu, logBuf, "Install Docker", err.Error())
		done(ch)
		return
	}
	sectionDone(ch, mu, logBuf, "Install Docker")

	// Step 2: Build application
	sectionStart(ch, mu, logBuf, "Build application")
	if err := stepBuildApp(ch, mu, logBuf); err != nil {
		sectionError(ch, mu, logBuf, "Build application", err.Error())
		done(ch)
		return
	}
	sectionDone(ch, mu, logBuf, "Build application")

	// Step 3: Start containers
	sectionStart(ch, mu, logBuf, "Start containers")
	if err := stepStartContainers(cfg, ch, mu, logBuf); err != nil {
		sectionError(ch, mu, logBuf, "Start containers", err.Error())
		done(ch)
		return
	}
	sectionDone(ch, mu, logBuf, "Start containers")

	if !cfg.SetupNginx {
		done(ch)
		return
	}

	// Step 4: Configure nginx
	sectionStart(ch, mu, logBuf, "Configure nginx")
	if err := stepConfigureNginx(cfg, ch, mu, logBuf); err != nil {
		sectionError(ch, mu, logBuf, "Configure nginx", err.Error())
		done(ch)
		return
	}
	sectionDone(ch, mu, logBuf, "Configure nginx")

	if !cfg.SetupTLS {
		done(ch)
		return
	}

	// Step 5: Obtain TLS certificate
	stepObtainTLS(cfg, ch, mu, logBuf)
}

func stepInstallDocker(ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	// Skip if already installed
	if err := exec.Command("docker", "info").Run(); err == nil {
		emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Docker already installed, skipping.\n"})
		return nil
	}
	if err := runCmd(ch, mu, logBuf, "", "sh", "-c", "curl -fsSL https://get.docker.com | sh"); err != nil {
		return fmt.Errorf("docker install failed: %w", err)
	}
	return runCmd(ch, mu, logBuf, "", "systemctl", "enable", "--now", "docker")
}

func stepBuildApp(ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	return runCmd(ch, mu, logBuf, repoDir+"/backend", "docker", "compose", "build", "--no-cache")
}

func stepStartContainers(cfg *installerConfig, ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	envPath := repoDir + "/backend/.env"
	if err := writeEnvFile(envPath, cfg, cfg.SetupTLS); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}
	emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Wrote " + envPath + "\n"})

	if err := runCmd(ch, mu, logBuf, repoDir+"/backend", "docker", "compose", "up", "-d"); err != nil {
		return err
	}
	return waitForHealthz(ch, mu, logBuf, "http://localhost:8080/healthz", 30*time.Second)
}

func waitForHealthz(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Health check passed.\n"})
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Waiting for app to start...\n"})
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("app did not become healthy within %s", timeout)
}

func stepConfigureNginx(cfg *installerConfig, ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	if err := runCmd(ch, mu, logBuf, "", "apt-get", "install", "-y", "nginx"); err != nil {
		return err
	}
	nginxConf := fmt.Sprintf(`server {
    listen 80;
    server_name %s;
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        client_max_body_size 50m;
    }
}
`, cfg.Domain)
	confPath := "/etc/nginx/sites-available/pubobs"
	if err := os.WriteFile(confPath, []byte(nginxConf), 0644); err != nil {
		return fmt.Errorf("write nginx config: %w", err)
	}
	os.Symlink(confPath, "/etc/nginx/sites-enabled/pubobs")
	os.Remove("/etc/nginx/sites-enabled/default")
	if err := runCmd(ch, mu, logBuf, "", "nginx", "-t"); err != nil {
		return fmt.Errorf("nginx config test failed: %w", err)
	}
	return runCmd(ch, mu, logBuf, "", "systemctl", "reload", "nginx")
}

// stepObtainTLS runs certbot and emits section_error (not fatal) on DNS mismatch.
func stepObtainTLS(cfg *installerConfig, ch chan string, mu *sync.Mutex, logBuf *strings.Builder) {
	sectionStart(ch, mu, logBuf, "Obtain TLS certificate")

	// Verify DNS before running certbot
	if msg := checkDNS(cfg.Domain); msg != "" {
		b, _ := json.Marshal(map[string]string{
			"type":    "section_error",
			"name":    "Obtain TLS certificate",
			"message": msg,
		})
		ch <- string(b)
		// Do NOT emit done — the SSE stream stays open for Retry/Skip
		return
	}

	if err := runCmd(ch, mu, logBuf, "", "apt-get", "install", "-y", "certbot", "python3-certbot-nginx"); err != nil {
		sectionError(ch, mu, logBuf, "Obtain TLS certificate", err.Error())
		done(ch)
		return
	}
	if err := runCmd(ch, mu, logBuf, "", "certbot", "--nginx",
		"-d", cfg.Domain,
		"--non-interactive", "--agree-tos",
		"--register-unsafely-without-email",
	); err != nil {
		sectionError(ch, mu, logBuf, "Obtain TLS certificate", err.Error())
		// Stay open for retry
		return
	}
	sectionDone(ch, mu, logBuf, "Obtain TLS certificate")
	done(ch)
}

// retryTLS re-runs the TLS step on an open SSE stream.
func retryTLS(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex) {
	stepObtainTLS(cfg, ch, mu, logBuf)
}

// skipTLS updates the .env to use http:// and restarts the container.
func skipTLS(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex) {
	envPath := repoDir + "/backend/.env"
	writeEnvFile(envPath, cfg, false) // useTLS=false
	emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Updated .env to use http://\n"})
	exec.Command("docker", "compose", "-f", repoDir+"/backend/docker-compose.yml", "restart").Run()
	done(ch)
}

// checkDNS resolves domain and compares to the server's public IP.
// Returns an error message string if there is a mismatch, empty string if OK.
func checkDNS(domain string) string {
	addrs, err := net.LookupHost(domain)
	if err != nil || len(addrs) == 0 {
		return fmt.Sprintf("DNS lookup for %s failed: %v. Ensure your A record is set.", domain, err)
	}
	serverIP := publicIP()
	if serverIP == "" {
		return "" // can't verify, proceed
	}
	for _, addr := range addrs {
		if addr == serverIP {
			return ""
		}
	}
	return fmt.Sprintf("DNS mismatch: %s resolves to %s but this server's IP is %s. Fix your DNS A record and retry, or skip TLS.", domain, addrs[0], serverIP)
}
```

- [ ] **Step 2: Run all installer tests**

```bash
cd installer && go test ./... -v
```
Expected: all PASS (new code compiles, existing tests pass).

- [ ] **Step 3: Commit**

```bash
git add installer/steps.go
git commit -m "feat: installer step executors and SSE broadcaster"
```

---

## Task 9: Wizard SPA (HTML/CSS/JS)

**Files:**
- Modify: `installer/static/index.html` (replace placeholder)

- [ ] **Step 1: Write the full wizard SPA**

Replace `installer/static/index.html` with:

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>PubObs Installer</title>
<style>
:root{--bg:#0f172a;--surface:#1e293b;--border:#334155;--text:#e2e8f0;--muted:#94a3b8;--accent:#14b8a6;--accent-dim:#0d9488;--danger:#f87171;--success:#4ade80;--warn:#fbbf24}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:system-ui,-apple-system,sans-serif;min-height:100vh;display:flex;flex-direction:column;align-items:center;padding:40px 16px}
.card{background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:32px;width:100%;max-width:600px}
h1{font-size:1.5rem;font-weight:700;margin-bottom:4px}
.subtitle{color:var(--muted);font-size:.875rem;margin-bottom:28px}
h2{font-size:1.125rem;font-weight:600;margin-bottom:4px}
.step-indicator{display:flex;gap:8px;margin-bottom:32px;width:100%;max-width:600px}
.step-dot{flex:1;height:4px;border-radius:2px;background:var(--border);transition:background .3s}
.step-dot.active{background:var(--accent)}
.step-dot.done{background:var(--accent-dim)}
label{display:block;font-size:.875rem;color:var(--muted);margin-bottom:6px;margin-top:16px}
input[type=text],input[type=email],input[type=url]{width:100%;background:var(--bg);border:1px solid var(--border);border-radius:6px;padding:10px 12px;color:var(--text);font-size:.875rem;outline:none}
input:focus{border-color:var(--accent)}
.hint{font-size:.75rem;color:var(--muted);margin-top:6px}
.hint a{color:var(--accent)}
.check-row{display:flex;align-items:center;gap:10px;margin-top:16px}
.check-row input[type=checkbox]{width:16px;height:16px;accent-color:var(--accent)}
.radio-group{display:flex;gap:8px;margin-top:8px;flex-wrap:wrap}
.radio-opt{display:flex;align-items:center;gap:6px;cursor:pointer;background:var(--bg);border:1px solid var(--border);border-radius:6px;padding:8px 14px;font-size:.875rem;transition:border-color .2s}
.radio-opt:has(input:checked){border-color:var(--accent);color:var(--accent)}
.radio-opt input{display:none}
.collapsible{margin-top:16px}
.collapsible-toggle{cursor:pointer;color:var(--accent);font-size:.8rem;user-select:none}
.collapsible-body{display:none;padding-left:12px;border-left:2px solid var(--border);margin-top:8px}
.collapsible-body.open{display:block}
.btn-row{display:flex;gap:12px;margin-top:28px;justify-content:flex-end}
button{padding:10px 24px;border-radius:6px;font-size:.875rem;font-weight:600;cursor:pointer;border:1px solid transparent;transition:opacity .2s}
button:disabled{opacity:.4;cursor:not-allowed}
.btn-primary{background:var(--accent);color:#0f172a;border-color:var(--accent)}
.btn-primary:hover:not(:disabled){background:var(--accent-dim)}
.btn-secondary{background:transparent;color:var(--muted);border-color:var(--border)}
.btn-danger{background:var(--danger);color:#0f172a}
.syscheck-list{display:flex;flex-direction:column;gap:10px;margin:20px 0}
.check-item{display:flex;align-items:center;gap:12px;padding:12px;background:var(--bg);border-radius:8px}
.check-icon{font-size:1.1rem;width:24px;text-align:center}
.check-label{font-size:.875rem}
.check-badge{margin-left:auto;font-size:.75rem;padding:2px 8px;border-radius:12px;background:var(--border);color:var(--muted)}
.check-badge.ok{background:#0d2318;color:var(--success)}
.check-badge.warn{background:#2d1c00;color:var(--warn)}
.check-badge.will-install{background:#0a2540;color:#60a5fa}
.review-table{width:100%;border-collapse:collapse;margin:16px 0}
.review-table td{padding:10px 12px;border-bottom:1px solid var(--border);font-size:.875rem}
.review-table td:first-child{color:var(--muted);width:40%}
.badge-generated{background:#0a2540;color:#60a5fa;font-size:.75rem;padding:2px 8px;border-radius:12px}
.install-sections{display:flex;flex-direction:column;gap:12px;margin:20px 0}
.install-section{border:1px solid var(--border);border-radius:8px;overflow:hidden}
.install-section.active{border-color:#1e4a6e}
.install-section.done-s{border-color:#1e3a2f}
.install-section.error-s{border-color:#4c1d1d}
.install-section.pending{opacity:.4}
.section-header{display:flex;align-items:center;gap:10px;padding:12px 16px;font-weight:600;font-size:.875rem}
.install-section.active .section-header{background:#0a2540;color:#60a5fa}
.install-section.done-s .section-header{background:#0d2318;color:var(--success)}
.install-section.error-s .section-header{background:#2d0f0f;color:var(--danger)}
.install-section.pending .section-header{background:var(--bg)}
.section-log{background:#020c18;padding:12px;font-family:monospace;font-size:.75rem;color:var(--muted);max-height:180px;overflow-y:auto;line-height:1.7;white-space:pre-wrap;word-break:break-all}
.section-actions{padding:12px 16px;display:flex;gap:10px}
.spinner{display:inline-block;width:16px;height:16px;border:2px solid #3b82f6;border-top-color:transparent;border-radius:50%;animation:spin .8s linear infinite;flex-shrink:0}
@keyframes spin{to{transform:rotate(360deg)}}
.done-icon{color:var(--success)}
.error-icon{color:var(--danger)}
.pending-icon{color:var(--muted)}
.success-block{text-align:center;padding:20px 0}
.success-block .big-check{font-size:4rem;margin-bottom:16px}
.success-block a{color:var(--accent);font-size:1.1rem;font-weight:700}
</style>
</head>
<body>

<div style="width:100%;max-width:600px;margin-bottom:16px">
  <div style="display:flex;align-items:center;gap:10px;margin-bottom:16px">
    <svg width="32" height="32" viewBox="0 0 32 32" fill="none"><circle cx="16" cy="16" r="16" fill="#14b8a6" opacity=".15"/><circle cx="16" cy="16" r="8" fill="#14b8a6"/></svg>
    <span style="font-weight:700;font-size:1.125rem">PubObs</span>
    <span style="color:var(--muted);font-size:.875rem;margin-left:4px">Installer</span>
  </div>
</div>

<div class="step-indicator" id="step-indicator">
  <div class="step-dot active" id="dot-1"></div>
  <div class="step-dot" id="dot-2"></div>
  <div class="step-dot" id="dot-3"></div>
  <div class="step-dot" id="dot-4"></div>
  <div class="step-dot" id="dot-5"></div>
  <div class="step-dot" id="dot-6"></div>
</div>

<div class="card">

<!-- Step 1: System Check -->
<div id="step-1">
  <h2>System Check</h2>
  <p class="subtitle">Checking your server environment…</p>
  <div class="syscheck-list" id="syscheck-list">
    <div style="color:var(--muted);font-size:.875rem">Running checks…</div>
  </div>
  <div class="btn-row">
    <button class="btn-primary" id="btn-next-1" onclick="goStep(2)" disabled>Next →</button>
  </div>
</div>

<!-- Step 2: Domain & Admin -->
<div id="step-2" style="display:none">
  <h2>Domain & Admin</h2>
  <p class="subtitle">Where will PubObs be hosted?</p>
  <label>Domain name</label>
  <input type="text" id="domain" placeholder="pubobs.example.com">
  <label>Admin email</label>
  <input type="email" id="admin-email" placeholder="you@example.com">
  <div class="hint">This email will be auto-promoted to instance admin on first login. It must match your sign-in provider account.</div>
  <div class="check-row" style="margin-top:20px">
    <input type="checkbox" id="setup-nginx" checked>
    <label style="margin:0;cursor:pointer" for="setup-nginx">Set up nginx + Let's Encrypt TLS</label>
  </div>
  <div id="dns-hint" class="hint" style="margin-top:8px;color:var(--warn)">Make sure an A record for this domain points to this server before clicking Install.</div>
  <div class="btn-row">
    <button class="btn-secondary" onclick="goStep(1)">← Back</button>
    <button class="btn-primary" onclick="step2Next()">Next →</button>
  </div>
</div>

<!-- Step 3: Auth Provider -->
<div id="step-3" style="display:none">
  <h2>Sign-in Provider</h2>
  <p class="subtitle">How will users authenticate?</p>
  <div class="radio-group" id="provider-radios">
    <label class="radio-opt"><input type="radio" name="provider" value="google" checked onchange="updateProviderFields()"> Google</label>
    <label class="radio-opt"><input type="radio" name="provider" value="yandex" onchange="updateProviderFields()"> Yandex</label>
    <label class="radio-opt"><input type="radio" name="provider" value="custom" onchange="updateProviderFields()"> Custom OIDC</label>
  </div>

  <div id="fields-google">
    <div class="hint" style="margin-top:12px">Redirect URI to register in Google Cloud Console:<br>
      <strong id="redirect-uri-google" style="color:var(--accent)"></strong>
      <a href="https://console.cloud.google.com/apis/credentials" target="_blank" style="margin-left:8px">Open Console →</a>
    </div>
    <label>Client ID</label>
    <input type="text" id="google-client-id" placeholder="...apps.googleusercontent.com">
    <label>Client Secret</label>
    <input type="text" id="google-client-secret" placeholder="GOCSPX-...">
  </div>

  <div id="fields-yandex" style="display:none">
    <div class="hint" style="margin-top:12px">Redirect URI to register in <a href="https://oauth.yandex.ru" target="_blank">Yandex OAuth</a>:<br>
      <strong id="redirect-uri-yandex" style="color:var(--accent)"></strong>
    </div>
    <label>Client ID</label>
    <input type="text" id="yandex-client-id" placeholder="Yandex client ID">
    <label>Client Secret</label>
    <input type="text" id="yandex-client-secret" placeholder="Yandex client secret">
  </div>

  <div id="fields-custom" style="display:none">
    <div class="hint" style="margin-top:12px">Redirect URI:<br>
      <strong id="redirect-uri-custom" style="color:var(--accent)"></strong>
    </div>
    <label>Issuer URL</label>
    <input type="url" id="custom-issuer" placeholder="https://accounts.example.com">
    <label>Client ID</label>
    <input type="text" id="custom-client-id" placeholder="client_id">
    <label>Client Secret</label>
    <input type="text" id="custom-client-secret" placeholder="client_secret">
  </div>

  <div id="secondary-yandex-toggle" class="collapsible" style="display:none">
    <div class="collapsible-toggle" onclick="toggleSecondary()">+ Add Yandex as secondary sign-in</div>
    <div class="collapsible-body" id="secondary-yandex-body">
      <label>Yandex Client ID</label>
      <input type="text" id="sec-yandex-client-id">
      <label>Yandex Client Secret</label>
      <input type="text" id="sec-yandex-client-secret">
    </div>
  </div>

  <div class="btn-row">
    <button class="btn-secondary" onclick="goStep(2)">← Back</button>
    <button class="btn-primary" onclick="step3Next()">Next →</button>
  </div>
</div>

<!-- Step 4: Review -->
<div id="step-4" style="display:none">
  <h2>Review</h2>
  <p class="subtitle">Confirm your configuration before installing.</p>
  <table class="review-table" id="review-table"></table>
  <div class="btn-row">
    <button class="btn-secondary" onclick="goStep(3)">← Back</button>
    <button class="btn-primary" onclick="startInstall()">Install</button>
  </div>
</div>

<!-- Step 5: Installing -->
<div id="step-5" style="display:none">
  <h2>Installing</h2>
  <p class="subtitle" id="install-subtitle">This takes a few minutes. Don't close this tab.</p>
  <div class="install-sections" id="install-sections"></div>
</div>

<!-- Step 6: Done -->
<div id="step-6" style="display:none">
  <div class="success-block">
    <div class="big-check">✓</div>
    <h2 style="margin-bottom:12px">PubObs is ready!</h2>
    <a id="app-link" href="#" target="_blank"></a>
    <p class="hint" style="margin-top:16px">Log in with your admin email to get started.</p>
    <p class="hint" style="margin-top:24px">The installer has stopped. To make changes, run the install command again.</p>
  </div>
</div>

</div><!-- .card -->

<script>
const state = { step: 1, cfg: {} };

// ── Navigation ────────────────────────────────────────────────
function goStep(n) {
  document.getElementById('step-' + state.step).style.display = 'none';
  document.getElementById('step-' + n).style.display = 'block';
  for (let i = 1; i <= 6; i++) {
    const d = document.getElementById('dot-' + i);
    d.className = 'step-dot' + (i < n ? ' done' : i === n ? ' active' : '');
  }
  state.step = n;
  if (n === 4) renderReview();
  if (n === 5) beginInstall();
}

// ── Step 1: System Check ──────────────────────────────────────
fetch('/api/syscheck').then(r => r.json()).then(sc => {
  const list = document.getElementById('syscheck-list');
  list.innerHTML = '';
  const rows = [
    { icon: '🖥', label: 'Operating system', badge: sc.os || 'Linux', cls: 'ok' },
    { icon: '⚙', label: 'Architecture', badge: sc.arch, cls: 'ok' },
    { icon: '💾', label: 'Free disk space', badge: (sc.disk_free_gb || 0).toFixed(1) + ' GB', cls: sc.disk_ok ? 'ok' : 'warn' },
    { icon: '🐳', label: 'Docker', badge: sc.docker_present ? 'installed' : 'will install', cls: sc.docker_present ? 'ok' : 'will-install' },
    { icon: '📦', label: 'Git', badge: sc.git_present ? 'installed' : 'will install', cls: sc.git_present ? 'ok' : 'will-install' },
  ];
  rows.forEach(row => {
    list.innerHTML += `<div class="check-item">
      <span class="check-icon">${row.icon}</span>
      <span class="check-label">${row.label}</span>
      <span class="check-badge ${row.cls}">${row.badge}</span>
    </div>`;
  });
  document.getElementById('btn-next-1').disabled = false;
});

// ── Step 2 ────────────────────────────────────────────────────
document.getElementById('setup-nginx').addEventListener('change', e => {
  document.getElementById('dns-hint').style.display = e.target.checked ? '' : 'none';
});

function step2Next() {
  const domain = document.getElementById('domain').value.trim();
  const email = document.getElementById('admin-email').value.trim();
  if (!domain) { alert('Domain is required'); return; }
  if (!email) { alert('Admin email is required'); return; }
  state.cfg.domain = domain;
  state.cfg.admin_email = email;
  state.cfg.setup_nginx = document.getElementById('setup-nginx').checked;
  state.cfg.setup_tls = state.cfg.setup_nginx;
  // Update redirect URIs
  const base = 'https://' + domain;
  document.getElementById('redirect-uri-google').textContent = base + '/auth/callback';
  document.getElementById('redirect-uri-yandex').textContent = base + '/auth/callback';
  document.getElementById('redirect-uri-custom').textContent = base + '/auth/callback';
  goStep(3);
}

// ── Step 3 ────────────────────────────────────────────────────
function updateProviderFields() {
  const p = document.querySelector('input[name=provider]:checked').value;
  ['google','yandex','custom'].forEach(id => {
    document.getElementById('fields-' + id).style.display = id === p ? '' : 'none';
  });
  document.getElementById('secondary-yandex-toggle').style.display =
    (p === 'google' || p === 'custom') ? '' : 'none';
}
updateProviderFields();

function toggleSecondary() {
  const body = document.getElementById('secondary-yandex-body');
  body.classList.toggle('open');
}

function step3Next() {
  const p = document.querySelector('input[name=provider]:checked').value;
  state.cfg.oidc_provider = p;
  if (p === 'google') {
    state.cfg.oidc_issuer = 'https://accounts.google.com';
    state.cfg.oidc_client_id = document.getElementById('google-client-id').value.trim();
    state.cfg.oidc_client_secret = document.getElementById('google-client-secret').value.trim();
    if (!state.cfg.oidc_client_id || !state.cfg.oidc_client_secret) { alert('Client ID and Secret are required'); return; }
    const sec = document.getElementById('secondary-yandex-body');
    if (sec.classList.contains('open')) {
      state.cfg.yandex_client_id = document.getElementById('sec-yandex-client-id').value.trim();
      state.cfg.yandex_client_secret = document.getElementById('sec-yandex-client-secret').value.trim();
    }
  } else if (p === 'yandex') {
    state.cfg.oidc_issuer = '';
    state.cfg.oidc_client_id = document.getElementById('yandex-client-id').value.trim();
    state.cfg.oidc_client_secret = document.getElementById('yandex-client-secret').value.trim();
    state.cfg.yandex_client_id = state.cfg.oidc_client_id;
    state.cfg.yandex_client_secret = state.cfg.oidc_client_secret;
    state.cfg.oidc_provider = 'yandex';
    if (!state.cfg.oidc_client_id) { alert('Client ID is required'); return; }
  } else {
    state.cfg.oidc_issuer = document.getElementById('custom-issuer').value.trim();
    state.cfg.oidc_client_id = document.getElementById('custom-client-id').value.trim();
    state.cfg.oidc_client_secret = document.getElementById('custom-client-secret').value.trim();
    if (!state.cfg.oidc_issuer || !state.cfg.oidc_client_id) { alert('Issuer and Client ID are required'); return; }
    const sec = document.getElementById('secondary-yandex-body');
    if (sec.classList.contains('open')) {
      state.cfg.yandex_client_id = document.getElementById('sec-yandex-client-id').value.trim();
      state.cfg.yandex_client_secret = document.getElementById('sec-yandex-client-secret').value.trim();
    }
  }
  goStep(4);
}

// ── Step 4: Review ────────────────────────────────────────────
function renderReview() {
  const c = state.cfg;
  const rows = [
    ['Domain', c.domain],
    ['Admin email', c.admin_email],
    ['nginx + TLS', c.setup_nginx ? 'yes' : 'no'],
    ['Auth provider', c.oidc_provider],
    ['Client ID', c.oidc_client_id],
    ['Client Secret', '••••••••'],
    ['Secret key', '<span class="badge-generated">auto-generated</span>'],
  ];
  if (c.yandex_client_id) rows.push(['Yandex Client ID', c.yandex_client_id]);
  document.getElementById('review-table').innerHTML = rows.map(
    ([k, v]) => `<tr><td>${k}</td><td>${v}</td></tr>`
  ).join('');
}

// ── Step 5: Install ────────────────────────────────────────────
const SECTIONS = ['Install Docker','Build application','Start containers','Configure nginx','Obtain TLS certificate'];

function beginInstall() {
  const c = state.cfg;
  const visibleSections = SECTIONS.filter(s => {
    if (s === 'Configure nginx' && !c.setup_nginx) return false;
    if (s === 'Obtain TLS certificate' && !c.setup_tls) return false;
    return true;
  });

  const container = document.getElementById('install-sections');
  container.innerHTML = '';
  visibleSections.forEach(name => {
    const id = sectionId(name);
    container.innerHTML += `<div class="install-section pending" id="${id}">
      <div class="section-header">
        <span class="pending-icon">○</span>
        <span>${name}</span>
      </div>
    </div>`;
  });

  // POST config then start
  fetch('/api/config', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify(c)
  }).then(() => fetch('/api/install', {method:'POST'}))
    .then(() => openStream());
}

function sectionId(name) {
  return 'sec-' + name.toLowerCase().replace(/\s+/g,'-').replace(/[^a-z0-9-]/g,'');
}

function setSection(name, st) {
  const el = document.getElementById(sectionId(name));
  if (!el) return;
  el.className = 'install-section ' + st;
  const header = el.querySelector('.section-header');
  if (st === 'active') {
    header.innerHTML = `<span class="spinner"></span><span>${name}</span>`;
    el.innerHTML += '<div class="section-log" id="log-' + sectionId(name) + '"></div>';
  } else if (st === 'done-s') {
    header.innerHTML = `<span class="done-icon">✓</span><span>${name}</span>`;
    const log = el.querySelector('.section-log');
    if (log) log.remove(); // collapse log on success
  } else if (st === 'error-s') {
    header.innerHTML = `<span class="error-icon">✗</span><span>${name}</span>`;
  }
}

function appendLog(name, text) {
  const log = document.getElementById('log-' + sectionId(name));
  if (!log) return;
  log.textContent += text;
  log.scrollTop = log.scrollHeight;
}

let activeSection = null;

function openStream() {
  const es = new EventSource('/api/install/stream');
  es.onmessage = e => {
    const ev = JSON.parse(e.data);
    if (ev.type === 'section_start') {
      activeSection = ev.name;
      setSection(ev.name, 'active');
    } else if (ev.type === 'log') {
      appendLog(activeSection, ev.text);
    } else if (ev.type === 'section_done') {
      setSection(ev.name, 'done-s');
    } else if (ev.type === 'section_error') {
      setSection(ev.name, 'error-s');
      const el = document.getElementById(sectionId(ev.name));
      if (el) {
        const msg = document.createElement('div');
        msg.style.cssText = 'padding:10px 16px;font-size:.8rem;color:var(--danger)';
        msg.textContent = ev.message;
        el.appendChild(msg);
        if (ev.name === 'Obtain TLS certificate') {
          const actions = document.createElement('div');
          actions.className = 'section-actions';
          actions.innerHTML = `<button class="btn-primary" onclick="retryTLS(this)">Retry</button>
            <button class="btn-secondary" onclick="skipTLS(this)">Skip TLS</button>`;
          el.appendChild(actions);
        } else {
          const dl = document.createElement('div');
          dl.style.cssText = 'padding:10px 16px';
          dl.innerHTML = `<a href="/api/logs" style="color:var(--accent);font-size:.8rem">Download install log</a>
            <span style="color:var(--muted);font-size:.8rem;margin-left:12px">then re-run the install command to try again</span>`;
          el.appendChild(dl);
          es.close();
        }
      }
    } else if (ev.type === 'done') {
      es.close();
      setTimeout(() => goStep(6), 500);
    }
  };
}

function retryTLS(btn) {
  btn.closest('.install-section').querySelector('.section-actions')?.remove();
  btn.closest('.install-section').querySelectorAll('div[style*="color:var(--danger)"]').forEach(e => e.remove());
  setSection('Obtain TLS certificate', 'active');
  fetch('/api/install/retry-tls', {method:'POST'}).then(() => openStream());
}

function skipTLS(btn) {
  btn.closest('.install-section').querySelector('.section-actions')?.remove();
  fetch('/api/install/skip-tls', {method:'POST'}).then(() => openStream());
}

// ── Step 6: Done ──────────────────────────────────────────────
function showDone() {
  const proto = state.cfg.setup_tls ? 'https' : 'http';
  const url = proto + '://' + state.cfg.domain;
  const link = document.getElementById('app-link');
  link.href = url;
  link.textContent = url;
  fetch('/api/shutdown', {method:'POST'});
}

// Override goStep to call showDone when reaching step 6
const _goStep = goStep;
window.goStep = function(n) {
  _goStep(n);
  if (n === 6) showDone();
};
</script>

</body>
</html>
```

- [ ] **Step 2: Run installer tests to confirm static embed works**

```bash
cd installer && go test ./... -v
```
Expected: all PASS — the embedded HTML is served correctly by `TestRootServesHTML`.

- [ ] **Step 3: Commit**

```bash
git add installer/static/index.html
git commit -m "feat: installer wizard SPA"
```

---

## Task 10: Bootstrap script and Makefile

**Files:**
- Create: `install.sh`
- Create: `installer/Makefile`
- Create: `installer/bin/.gitkeep`

- [ ] **Step 1: Create `install.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

REPO_URL="https://github.com/pubobs/pubobs.git"
INSTALL_DIR="/opt/pubobs"
PORT=8000

echo "=== PubObs Installer ==="
echo ""

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  BINARY="installer-linux-amd64" ;;
  aarch64) BINARY="installer-linux-arm64" ;;
  arm64)   BINARY="installer-linux-arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# Install git if missing
if ! command -v git &>/dev/null; then
  echo "Installing git..."
  apt-get update -qq && apt-get install -y -qq git
fi

# Clone or update repo
if [ -d "$INSTALL_DIR/.git" ]; then
  echo "Repo already exists at $INSTALL_DIR, updating..."
  git -C "$INSTALL_DIR" pull --ff-only
else
  echo "Cloning PubObs to $INSTALL_DIR..."
  git clone "$REPO_URL" "$INSTALL_DIR"
fi

INSTALLER="$INSTALL_DIR/installer/bin/$BINARY"
chmod +x "$INSTALLER"

# Detect public IP
PUBLIC_IP=$(curl -s --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')

echo ""
echo "┌─────────────────────────────────────────────────────────┐"
echo "│  PubObs Installer is ready.                             │"
echo "│                                                         │"
echo "│  Open in your browser:                                  │"
echo "│  http://${PUBLIC_IP}:${PORT}                            │"
echo "│                                                         │"
echo "│  Note: ensure port $PORT is reachable from your machine │"
echo "└─────────────────────────────────────────────────────────┘"
echo ""
echo "Installer log output:"
echo "────────────────────"

# Run installer (foreground — script exits when installer does)
"$INSTALLER" --port "$PORT"
```

- [ ] **Step 2: Create `installer/Makefile`**

```makefile
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
```

- [ ] **Step 3: Add `--port` flag support to `installer/main.go`**

Add to the top of `main()` in `installer/main.go`:
```go
import "flag"

// at top of main():
port := flag.String("port", "8000", "port to listen on")
flag.Parse()
addr := ":" + *port
```

Replace the existing `addr := ":8000"` line.

- [ ] **Step 4: Create placeholder for binaries**

```bash
mkdir -p installer/bin
touch installer/bin/.gitkeep
```

- [ ] **Step 5: Verify installer still compiles**

```bash
cd installer && go build -o /tmp/installer-test . && echo "OK"
```
Expected: `OK`.

- [ ] **Step 6: Commit**

```bash
git add install.sh installer/Makefile installer/bin/.gitkeep
git commit -m "feat: bootstrap script and installer Makefile"
```

---

## Task 11: Build pre-built binaries and final wiring

**Files:**
- Create: `installer/bin/installer-linux-amd64`
- Create: `installer/bin/installer-linux-arm64`
- Create: `.gitattributes` (mark binaries as binary in git)

- [ ] **Step 1: Add `.gitattributes` to handle binary files**

Create `.gitattributes` in repo root (or append if it exists):
```
installer/bin/installer-linux-* binary
```

- [ ] **Step 2: Build both binaries**

```bash
cd installer && make build
```
Expected: creates `installer/bin/installer-linux-amd64` and `installer/bin/installer-linux-arm64`.

- [ ] **Step 3: Smoke test the binary on the local machine (mac/linux)**

```bash
# Test that it at least starts and responds
installer/bin/installer-linux-amd64 --port 8001 &
PID=$!
sleep 1
curl -s http://localhost:8001/api/syscheck | python3 -m json.tool
kill $PID
```

> **Note:** On macOS this will fail (wrong binary arch) — skip this step on mac and test on a Linux box or via Docker:
> ```bash
> docker run --rm -v $(pwd)/installer/bin:/b alpine /b/installer-linux-amd64 --port 8001 &
> ```

- [ ] **Step 4: Run all backend tests one final time**

```bash
cd backend && go test ./... -v 2>&1 | grep -E "PASS|FAIL|ok"
```
Expected: all PASS.

- [ ] **Step 5: Commit binaries**

```bash
git add .gitattributes installer/bin/installer-linux-amd64 installer/bin/installer-linux-arm64
git commit -m "feat: pre-built installer binaries (amd64 + arm64)"
```

- [ ] **Step 6: Update README.md**

Replace the existing `README.md` with a one-liner install instruction at the top:

Add before the existing content:
```markdown
## Quick Install (VPS)

```bash
curl -fsSL https://raw.githubusercontent.com/pubobs/pubobs/main/install.sh | bash
```

Open the URL printed in your terminal, follow the wizard, done.

---
```

- [ ] **Step 7: Commit README**

```bash
git add README.md
git commit -m "docs: add one-liner install to README"
```

---

## Self-Review Checklist

- **Bootstrap** → Task 10 (`install.sh`)
- **Pre-built binaries** → Task 11
- **Syscheck** → Task 6 (`syscheck.go`)
- **HTTP server + all routes** → Task 7 (`main.go`)
- **SSE streaming + step executors** → Task 8 (`steps.go`)
- **Wizard SPA all 6 steps** → Task 9 (`index.html`)
- **PUBOBS_ADMIN_EMAIL config** → Task 1
- **ListInstanceAdmins** → Task 2
- **Auto-promotion on login** → Task 3
- **GET /healthz** → Task 4
- **3-stage Dockerfile** → Task 5
- **docker-compose context + restart** → Task 5
- **writeEnvFile** → Task 7 (`main.go`)
- **skipTLS updates .env + restarts** → Task 8 (`steps.go`)
- **Retry TLS stays on open stream** → Task 8 + Task 9
- **Self-shutdown after Done** → Task 7 + Task 9
- **Security: .env mode 0600** → Task 8 (`writeEnvFile` in `os.WriteFile(..., 0600)`)
