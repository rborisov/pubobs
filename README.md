## Quick Install (VPS)

```bash
curl -fsSL https://raw.githubusercontent.com/rborisov/pubobs/main/install.sh -o /tmp/pubobs-install.sh
sudo bash /tmp/pubobs-install.sh
```

The installer sets up Docker, configures nginx, and obtains a TLS certificate automatically. Follow the prompts.

---

# PubObs — VPS Deployment Guide

PubObs is a self-hosted publishing platform for Obsidian notes. It clones your Git repositories, serves notes to readers, and handles authentication via OIDC.

---

## Prerequisites

- A VPS running Ubuntu/Debian (or any Linux distro with `apt`)
- A domain name pointing to your VPS (e.g. `pubobs.example.com`)
- Root or sudo access

The installer handles everything else (Docker, nginx, Certbot).

---

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/rborisov/pubobs/main/install.sh -o /tmp/pubobs-install.sh
sudo bash /tmp/pubobs-install.sh
```

You will be prompted for:

- Domain name
- Admin email
- OIDC provider credentials (Google, Yandex, or custom)
- Whether to configure nginx and TLS automatically

At the end, the installer prints the URL where PubObs is running.

---

## Update

Download the latest installer and run with `--update`:

```bash
curl -fsSL https://raw.githubusercontent.com/rborisov/pubobs/main/install.sh -o /tmp/pubobs-install.sh
sudo bash /tmp/pubobs-install.sh --update
```

This pulls the latest binary from GitHub, rebuilds the Docker image, and restarts the container. Your data and `.env` are untouched. Once the new container is confirmed healthy, the installer also prunes dangling Docker images and stale build cache left over from the rebuild — useful on small VPS instances where disk space is tight.

> **Note:** Piping directly to bash (`curl | bash --update`) does not work — save the script first, then run it.

---

## Reinstall (preserve data)

```bash
curl -fsSL https://raw.githubusercontent.com/rborisov/pubobs/main/install.sh -o /tmp/pubobs-install.sh
sudo bash /tmp/pubobs-install.sh --reinstall
```

You will be asked whether to keep or wipe the database and repo cache. Like `--update`, it prunes leftover Docker images/build cache once the reinstalled container is healthy.

---

## Auth Setup

PubObs requires an OIDC provider. The installer asks interactively, but you can also edit `/opt/pubobs/backend/.env` afterwards.

### Google

1. [Google Cloud Console](https://console.cloud.google.com) → APIs & Services → Credentials → Create OAuth 2.0 Client ID (Web application)
2. Authorized redirect URI: `https://pubobs.example.com/auth/oidc/callback`
3. Set in `.env`:

   ```env
   PUBOBS_OIDC_ISSUER=https://accounts.google.com
   PUBOBS_OIDC_CLIENT_ID=....apps.googleusercontent.com
   PUBOBS_OIDC_CLIENT_SECRET=GOCSPX-...
   ```

### Yandex (optional second provider)

1. [Yandex OAuth](https://oauth.yandex.ru) → Create app → callback: `https://pubobs.example.com/auth/yandex/callback`
2. Set in `.env`:

   ```env
   PUBOBS_YANDEX_CLIENT_ID=...
   PUBOBS_YANDEX_CLIENT_SECRET=...
   ```

### Self-hosted (Authentik / Keycloak / Dex)

Set `PUBOBS_OIDC_ISSUER` to your provider's issuer URL and configure the redirect URI as above.

---

## Environment variables

Full list of supported variables in `/opt/pubobs/backend/.env`:

```env
# Required
PUBOBS_BASE_URL=https://pubobs.example.com   # no trailing slash
PUBOBS_OIDC_ISSUER=https://accounts.google.com
PUBOBS_OIDC_CLIENT_ID=...
PUBOBS_OIDC_CLIENT_SECRET=...
PUBOBS_SECRET_KEY=...                        # openssl rand -hex 32

# Optional
PUBOBS_ADMIN_EMAIL=admin@example.com         # grants admin on first login
PUBOBS_YANDEX_CLIENT_ID=
PUBOBS_YANDEX_CLIENT_SECRET=

# Render storage (bootstrap default only — see Storage section below)
PUBOBS_RENDER_STORE=local                    # or "s3"
PUBOBS_RENDER_DIR=/data/renders              # used when RENDER_STORE=local
PUBOBS_ASSET_DIR=/data/assets                # used when RENDER_STORE=local

# S3-compatible render storage (bootstrap default only, required when RENDER_STORE=s3)
PUBOBS_S3_ENDPOINT=s3.amazonaws.com
PUBOBS_S3_BUCKET=my-pubobs-renders
PUBOBS_S3_ACCESS_KEY=...
PUBOBS_S3_SECRET_KEY=...
PUBOBS_S3_REGION=us-east-1
PUBOBS_S3_USE_SSL=true

# Tuning
PUBOBS_PORT=8080
PUBOBS_REPO_CACHE_TTL=24h
PUBOBS_CACHE_CHECK_INTERVAL=1h
PUBOBS_DISK_WARN_PCT=20
PUBOBS_DISK_CRIT_PCT=5
```

After editing `.env`, restart the container:

```bash
cd /opt/pubobs/backend
docker compose restart
```

---

## Storage

Each repo's render blobs and media assets can live on local disk or on an
S3-compatible object storage destination, chosen per repo. Git checkouts
themselves always stay on local disk regardless of this setting.

The `PUBOBS_RENDER_STORE` / `PUBOBS_S3_*` / `PUBOBS_ASSET_DIR` environment
variables above are **bootstrap-only**: they seed the database the first
time the server starts with an empty `storage_settings` table, and are
ignored on every subsequent boot. If they described an S3 configuration,
that configuration becomes a **default** S3 destination and every existing
repo is assigned to it; with no S3 configuration, repos default to local
storage.

After first boot, manage storage from the admin panel:

- The **Storage** page (instance admin nav) manages the list of S3
  destinations available instance-wide — add, edit, or remove S3
  credentials and view per-destination usage. Changes apply live — no
  container restart needed.
- Each repo's detail page lets you pick that repo's storage: **Local** or
  any configured S3 destination. Switching a repo's destination migrates
  its existing renders and assets to the new location as a background job;
  the page reflects progress and updated usage once the migration
  completes.

---

## File layout on VPS

```text
/opt/pubobs/
└── backend/
    ├── Dockerfile
    ├── docker-compose.yml
    ├── .env                  ← your secrets (never commit this)
    └── data/
        ├── db/pubobs.db      ← SQLite database
        ├── repos/            ← cloned repo cache
        ├── renders/          ← encrypted render blobs
        └── assets/           ← media assets (images, etc.)
```

---

## Obsidian plugin

### Install via BRAT (recommended)

[BRAT](https://github.com/TfTHacker/obsidian42-brat) lets you install and auto-update beta plugins from GitHub.

1. Install the **BRAT** community plugin in Obsidian
2. BRAT settings → **Add Beta Plugin** → enter `rborisov/pubobs`
3. Enable **PubObs** in Obsidian's community plugins list

### Manual install

Download `main.js` and `manifest.json` from the [latest release](https://github.com/rborisov/pubobs/releases/latest) and copy them to `.obsidian/plugins/pubobs/` in your vault.

---

## Development

This section is for building and deploying PubObs from source. If you just want to run PubObs, use the [Quick Install](#quick-install-vps) instructions above instead.

### Server (backend + frontend)

Prerequisites: Go 1.25+, Node 20+.

The backend serves a bundled frontend (`backend/frontend/static/`) via `go:embed`, and its Docker image doesn't compile Go — `backend/Dockerfile` just copies a prebuilt binary from `backend/bin/`. That means **the binaries in `backend/bin/` are committed to git** and must be rebuilt and committed as part of any change that touches the frontend or backend source.

1. Build the frontend (regenerates `backend/frontend/static/app.js`, embedded into the binary at compile time):

   ```bash
   cd frontend
   npm install
   npm run build
   ```

2. Build the Linux binaries the Docker image copies in:

   ```bash
   cd backend
   make build          # builds bin/pubobs-linux-amd64 and bin/pubobs-linux-arm64
   ```

3. Commit the source changes *together with* the rebuilt `backend/frontend/static/app.js`, `backend/bin/pubobs-linux-amd64`, and `backend/bin/pubobs-linux-arm64`, then push to `main`.

4. Deploy by running the updater on the VPS — it pulls `main`, copies the new binary, rebuilds the Docker image, and restarts the container:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/rborisov/pubobs/main/install.sh -o /tmp/pubobs-install.sh
   sudo bash /tmp/pubobs-install.sh --update
   ```

For local iteration without a full deploy, `cd frontend && npm run dev` runs an unminified esbuild watch build, and `cd backend && go run ./cmd/server` runs the server directly (needs the same env vars as [Environment variables](#environment-variables)).

### Obsidian plugin release

Prerequisites: Node 20+.

```bash
cd obsidian-plugin
npm install
npm run dev      # watch mode, for local testing in a vault
npm run build    # typecheck + production build -> main.js
```

Releases are automated by [`.github/workflows/release-plugin.yml`](.github/workflows/release-plugin.yml): pushing a tag matching `[0-9]+.[0-9]+.[0-9]+` (e.g. `0.3.0`) builds the plugin and publishes a GitHub release with `main.js` and `manifest.json` attached, which is what BRAT and manual installs pull from.

To cut a release:

1. Bump `"version"` in **both** `obsidian-plugin/package.json` and `obsidian-plugin/manifest.json` to the new version, commit, push to `main`.
2. Tag and push the tag: `git tag 0.3.0 && git push origin 0.3.0`.

> **Note:** past releases (e.g. `0.2.1`–`0.2.5`) were tagged without bumping `manifest.json`'s `version` field, which still reads `0.2.0` at those tags — keep the two in sync going forward so the version Obsidian displays matches the actual release.

---

## Troubleshooting

**Container won't start:**

```bash
cd /opt/pubobs/backend && docker compose logs pubobs
```

**OIDC login fails:**

- Verify the redirect URI in your provider matches exactly: `https://your-domain/auth/oidc/callback`
- Check `PUBOBS_BASE_URL` has no trailing slash

**"PUBOBS_SECRET_KEY must be 64 hex chars":**
```bash
openssl rand -hex 32   # produces exactly 64 hex characters
```

**Reset everything (wipes all data):**
```bash
cd /opt/pubobs/backend
docker compose down
rm -rf data/
docker compose up -d --build
```
