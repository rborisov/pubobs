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

This pulls the latest binary from GitHub, rebuilds the Docker image, and restarts the container. Your data and `.env` are untouched.

> **Note:** Piping directly to bash (`curl | bash --update`) does not work — save the script first, then run it.

---

## Reinstall (preserve data)

```bash
curl -fsSL https://raw.githubusercontent.com/rborisov/pubobs/main/install.sh -o /tmp/pubobs-install.sh
sudo bash /tmp/pubobs-install.sh --reinstall
```

You will be asked whether to keep or wipe the database and repo cache.

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

# Render storage (default: local disk)
PUBOBS_RENDER_STORE=local                    # or "s3"
PUBOBS_RENDER_DIR=/data/renders              # used when RENDER_STORE=local

# S3-compatible render storage (required when RENDER_STORE=s3)
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
        └── renders/          ← encrypted render blobs
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
