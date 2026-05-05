# PubObs Installer Webapp — Design Spec

**Date:** 2026-05-05  
**Status:** Approved

---

## Overview

A self-contained installation wizard that lets a user deploy PubObs on a fresh Ubuntu VPS by pasting one command into their terminal. The installer is a pre-built Go binary that serves a browser-based wizard, collects required configuration, then executes the full installation automatically.

---

## Bootstrap Entry Point

**File:** `install.sh` (repo root)

The user runs:
```bash
curl -fsSL https://raw.githubusercontent.com/pubobs/pubobs/main/install.sh | bash
```

The script does the following in order:
1. Detects CPU architecture (`uname -m` → `amd64` or `arm64`)
2. Installs `git` if missing (`apt-get install -y git`)
3. Clones the repo to `/opt/pubobs` (skips if already exists)
4. Makes the appropriate installer binary executable: `installer/bin/installer-linux-{arch}`
5. Launches the binary in the background, bound to `0.0.0.0:8000`
6. Prints: `"PubObs Installer is running. Open http://<server-ip>:8000 in your browser."`
7. Tails the installer log so the terminal shows live output
8. Exits when the installer process exits

The script detects the server's public IP using `curl -s https://api.ipify.org` for display in the message.

---

## Installer Binary

**Source:** `installer/` (Go package)  
**Binaries:** `installer/bin/installer-linux-amd64`, `installer/bin/installer-linux-arm64`  
**Build:** `installer/Makefile` — cross-compiles both targets with `CGO_ENABLED=0`

The binary:
- Runs as root (required for Docker install, nginx, certbot)
- Serves HTTP on `:8000`
- Embeds all wizard HTML/CSS/JS via `//go:embed`
- Maintains wizard state in memory (no persistence needed — one-shot process)
- Self-terminates 3 seconds after the Done screen loads

### HTTP Routes

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | Wizard SPA (single embedded HTML file) |
| `GET` | `/api/syscheck` | System check results (JSON) |
| `POST` | `/api/config` | Save wizard config to memory |
| `POST` | `/api/install` | Start installation, returns 200 immediately |
| `GET` | `/api/install/stream` | SSE stream of install progress (stays open until `done` event) |
| `POST` | `/api/install/retry-tls` | Re-run TLS step; emits new events on open SSE stream |
| `POST` | `/api/install/skip-tls` | Skip TLS; emits `{"type":"done"}` on open SSE stream |
| `GET` | `/api/logs` | Download full accumulated log as plain text (for error reporting) |
| `POST` | `/api/shutdown` | Called by frontend on Done screen; exits after 3s |

### In-Memory State

```go
type InstallerState struct {
    Config        *Config
    InstallStatus string // idle | running | done | error
}

type Config struct {
    Domain             string
    AdminEmail         string
    SetupNginx         bool
    SetupTLS           bool
    OIDCProvider       string // google | yandex | custom
    OIDCIssuer         string
    OIDCClientID       string
    OIDCClientSecret   string
    YandexClientID     string
    YandexClientSecret string
    SecretKey          string // auto-generated, 32 random bytes as hex
}
```

---

## Wizard Steps

### Step 1 — Welcome + System Check

No user input. The frontend calls `GET /api/syscheck` on load.

The backend checks and returns JSON:
- OS and version
- Architecture
- Docker: installed / will install
- Available disk space (warn if < 5 GB)
- Git: installed / will install

Each check renders as a status row (green tick or "will install" badge). The **Next** button is enabled once the JSON response arrives.

### Step 2 — Domain & Admin

Fields:
- **Domain** (text input, required) — e.g. `pubobs.example.com`
- **Admin email** (text input, required) — the email address that will be auto-promoted to instance admin on first login. Must match the email in the OIDC provider account.
- **Set up nginx + Let's Encrypt** (checkbox, default: checked)

If nginx checkbox is checked, a hint appears: *"Make sure an A record for this domain points to this server before clicking Install."*

### Step 3 — Auth Provider

Radio selection: **Google** / **Yandex** / **Custom OIDC**

Fields shown per provider:

**Google:**
- Client ID
- Client Secret
- Read-only hint: *Redirect URI to register:* `https://{domain}/auth/oidc/callback`
- Link: [Google Cloud Console →]

**Yandex:**
- Client ID  
- Client Secret
- Read-only hint: *Redirect URI:* `https://{domain}/auth/yandex/callback`

**Custom OIDC:**
- Issuer URL
- Client ID
- Client Secret
- Read-only hint: *Redirect URI:* `https://{domain}/auth/oidc/callback`

For Google and Custom OIDC, an optional collapsible section: **"Add Yandex as a second sign-in option"** — reveals Yandex Client ID + Secret fields.

### Step 4 — Review

Summary table of all collected values. Secret key shown as an `auto-generated` badge (generated on first render of this step, stored in `Config.SecretKey`).

**Back** button returns to Step 3.  
**Install** button POSTs to `/api/config` then `/api/install`, then navigates to Step 5.

### Step 5 — Installing

The frontend opens an SSE connection to `/api/install/stream` immediately on page load.

Five sections render in order; each transitions through states: **pending → active (spinner) → done (✓) → error (✗)**:

1. Install Docker
2. Build application
3. Start containers
4. Configure nginx *(skipped if nginx checkbox was off)*
5. Obtain TLS certificate *(skipped if TLS was off)*

Below each active section, a monospace log box streams raw command output.

**Error handling:**
- If any step fails, it turns red and displays the relevant log output with an error message.
- "Obtain TLS certificate" failure shows **Retry** + **Skip TLS** buttons (DNS may not be propagated yet).
- All other step failures show **Download logs** + a message to file an issue. No retry — the user must re-run the bootstrap.

### Step 6 — Done

- "PubObs is running at `https://{domain}`" (link)
- "The installer has stopped. To make changes, run the install command again."
- Calls `POST /api/shutdown` after 3 seconds; the binary exits.

---

## SSE Streaming Protocol

Each event is a `data:` line with a JSON payload:

```jsonl
data: {"type":"section_start","name":"Install Docker"}
data: {"type":"log","text":"Fetching https://get.docker.com...\n"}
data: {"type":"log","text":"Docker version 26.1.4 installed.\n"}
data: {"type":"section_done","name":"Install Docker"}
data: {"type":"section_start","name":"Build application"}
data: {"type":"log","text":"Step 1/8 : FROM node:22-alpine AS frontend\n"}
...
data: {"type":"section_error","name":"Obtain TLS certificate","message":"DNS mismatch: domain resolves to 1.2.3.4 but this server is 5.6.7.8"}
data: {"type":"done"}
```

The frontend maps section names to UI rows and appends `log` text to the active section's log box.

---

## Install Execution Steps

Executed sequentially in a goroutine. Each step pipes stdout+stderr through the SSE channel.

### 1. Install Docker
```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker pubobs  # not needed since installer runs as root
systemctl enable --now docker
```
Skipped if `docker info` exits 0.

### 2. Build application
```bash
cd /opt/pubobs/backend
docker compose build --no-cache
```
This uses the updated 3-stage Dockerfile (see below) — Node builds the frontend, Go embeds it.

### 3. Start containers
Write `.env` file to `/opt/pubobs/backend/.env`, then:
```bash
cd /opt/pubobs/backend
docker compose up -d
```
Health check: poll `GET http://localhost:8080/healthz` up to 30s, retrying every 2s.

> **Note:** A `GET /healthz → 200 OK` endpoint must be added to the backend router as part of this feature. It is a one-liner returning `{"ok":true}`.

### 4. Configure nginx
```bash
apt-get install -y nginx
```
Write config to `/etc/nginx/sites-available/pubobs` (HTTP only at this stage):
```nginx
server {
    listen 80;
    server_name {domain};
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
```
```bash
ln -sf /etc/nginx/sites-available/pubobs /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

### 5. Obtain TLS certificate
Before running certbot, verify DNS: resolve `{domain}` and compare to the server's public IP (from `api.ipify.org`). If they differ, emit a descriptive error with the Retry/Skip buttons.

```bash
apt-get install -y certbot python3-certbot-nginx
certbot --nginx -d {domain} --non-interactive --agree-tos --register-unsafely-without-email
```
Certbot updates the nginx config to add HTTPS and sets up auto-renewal.

---

## Backend Changes

### `PUBOBS_ADMIN_EMAIL` config field

Add `AdminEmail string` to `config.Config`, loaded from `PUBOBS_ADMIN_EMAIL` env var. Optional — if empty, no auto-promotion occurs.

### Auto-promotion on first login

In `internal/api/auth.go`, after `UpsertUser` succeeds in the OIDC/Yandex callback, add:

```go
if cfg.AdminEmail != "" && user.Email == cfg.AdminEmail && !user.IsInstanceAdmin {
    // Only promote if no instance admins exist yet
    admins, _ := deps.Store.ListInstanceAdmins(ctx)
    if len(admins) == 0 {
        deps.Store.SetInstanceAdmin(ctx, user.ID, true)
        user.IsInstanceAdmin = true
    }
}
```

Add `Store.ListInstanceAdmins(ctx) ([]*model.User, error)` — a simple query: `SELECT ... FROM users WHERE is_instance_admin=1`.

### `.env` written by installer

The installer writes `PUBOBS_ADMIN_EMAIL={adminEmail}` to `/opt/pubobs/backend/.env` alongside the other variables.

---

## Dockerfile Changes

The existing `backend/Dockerfile` becomes a 3-stage build. The docker-compose build context changes from `backend/` to the repo root so the frontend source is accessible.

**`backend/Dockerfile`** (updated):
```dockerfile
# Stage 1: Build frontend
FROM node:22-alpine AS frontend
WORKDIR /app
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN mkdir -p /backend/frontend/static
RUN npx tsc --noEmit -p tsconfig.json && node esbuild.config.mjs production
# esbuild.config.mjs outputs to ../backend/frontend/static/app.js (relative to WORKDIR /app)
# which resolves to /backend/frontend/static/app.js inside this stage

# Stage 2: Build backend
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

**`backend/docker-compose.yml`** (updated build context):
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
      PUBOBS_YANDEX_CLIENT_ID: ${PUBOBS_YANDEX_CLIENT_ID:-}
      PUBOBS_YANDEX_CLIENT_SECRET: ${PUBOBS_YANDEX_CLIENT_SECRET:-}
      PUBOBS_REPO_CACHE_TTL: 24h
      PUBOBS_CACHE_CHECK_INTERVAL: 1h
      PUBOBS_DISK_WARN_PCT: 20
      PUBOBS_DISK_CRIT_PCT: 5
```

Note: port binding changes to `127.0.0.1:8080:8080` so the app is not directly reachable from outside — only through nginx.

---

## File Layout

```
/opt/pubobs/                          ← cloned by install.sh
├── install.sh                        ← bootstrap (curl | bash)
├── installer/
│   ├── main.go                       ← HTTP server, embed, state
│   ├── steps.go                      ← install step executors
│   ├── syscheck.go                   ← system check logic
│   ├── static/
│   │   └── index.html                ← wizard SPA (embedded)
│   ├── Makefile                      ← cross-compile both arches
│   └── bin/
│       ├── installer-linux-amd64     ← pre-built binary (checked in)
│       └── installer-linux-arm64     ← pre-built binary (checked in)
├── backend/
│   ├── Dockerfile                    ← updated: 3-stage build
│   ├── docker-compose.yml            ← updated: context .., restart policy, 127.0.0.1 bind
│   └── ...
└── frontend/
    └── ...
```

---

## Security Considerations

- The installer binds to `0.0.0.0:8000`. The bootstrap script warns the user to restrict access with a firewall rule (`ufw allow from <your-ip> to any port 8000`) if the VPS has a public IP without firewall.
- No authentication on the installer itself — it's designed to run for minutes, not hours.
- The `.env` file is written with mode `0600` (owner-read only).
- After the installer exits, port 8000 is closed. There is no persistent installer process.
- The app port is bound to `127.0.0.1` only — not reachable except through nginx.
