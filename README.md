## Quick Install (VPS)

```bash
curl -fsSL https://raw.githubusercontent.com/pubobs/pubobs/main/install.sh | bash
```

Open the URL printed in your terminal and follow the wizard. The installer sets up Docker, builds the app, configures nginx, and obtains a TLS certificate automatically.

---

# PubObs — VPS Deployment Guide

PubObs is a self-hosted publishing platform for Obsidian notes. It clones your Git repositories, serves notes to readers, and handles authentication via OIDC.

---

## Prerequisites

- A VPS running Ubuntu/Debian (or any Linux distro)
- A domain name pointing to your VPS (e.g. `pubobs.example.com`)
- Docker and Docker Compose installed on the VPS
- An OIDC provider (see [Auth Setup](#auth-setup) below)
- Node.js 18+ on your **local machine** (to build the frontend)

Install Docker on the VPS:

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER   # log out and back in after this
```

---

## Step 1 — Build the frontend (local machine)

The Go binary embeds the frontend. Build it before deploying:

```bash
cd frontend
npm install
npm run build   # outputs to backend/frontend/static/app.js
```

---

## Step 2 — Transfer files to VPS

Push to Git and pull on the VPS, or copy directly:

```bash
# Option A: git
ssh user@your-vps "git clone https://github.com/you/pubobs /opt/pubobs"

# Option B: rsync (if repo is private or not on GitHub)
rsync -av --exclude=node_modules --exclude=.git . user@your-vps:/opt/pubobs
```

---

## Step 3 — Configure environment

On the VPS, create `/opt/pubobs/backend/.env`:

```bash
ssh user@your-vps
cd /opt/pubobs/backend
cp .env.example .env  # or create from scratch
nano .env
```

Required variables:

```env
# Your public URL (no trailing slash)
PUBOBS_BASE_URL=https://pubobs.example.com

# OIDC provider (see Auth Setup below)
PUBOBS_OIDC_ISSUER=https://accounts.google.com
PUBOBS_OIDC_CLIENT_ID=your-client-id
PUBOBS_OIDC_CLIENT_SECRET=your-client-secret

# Generate once: openssl rand -hex 32
PUBOBS_SECRET_KEY=your64hexchars0000000000000000000000000000000000000000000000000000
```

Optional variables:

```env
# Yandex OAuth (optional second sign-in button)
PUBOBS_YANDEX_CLIENT_ID=
PUBOBS_YANDEX_CLIENT_SECRET=

# Tuning (defaults shown)
PUBOBS_PORT=8080
PUBOBS_REPO_CACHE_TTL=24h
PUBOBS_CACHE_CHECK_INTERVAL=1h
PUBOBS_DISK_WARN_PCT=20
PUBOBS_DISK_CRIT_PCT=5
```

Generate a secret key:

```bash
openssl rand -hex 32
```

---

## Step 4 — Start the application

```bash
cd /opt/pubobs/backend
docker compose up -d --build
```

Check it's running:

```bash
docker compose logs -f
```

The app listens on `http://localhost:8080`. Data is persisted in `./data/`.

To start automatically on reboot, add a restart policy to `docker-compose.yml`:

```yaml
services:
  pubobs:
    restart: unless-stopped
    ...
```

---

## Step 5 — Set up nginx + HTTPS

Install nginx and Certbot:

```bash
sudo apt install -y nginx certbot python3-certbot-nginx
```

Create `/etc/nginx/sites-available/pubobs`:

```nginx
server {
    listen 80;
    server_name pubobs.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # For large repo clones
        proxy_read_timeout 120s;
        proxy_send_timeout 120s;
        client_max_body_size 50m;
    }
}
```

Enable and get a certificate:

```bash
sudo ln -s /etc/nginx/sites-available/pubobs /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo certbot --nginx -d pubobs.example.com
```

Certbot will auto-renew and update the nginx config for HTTPS.

---

## Auth Setup

PubObs requires an OIDC provider. Pick one:

### Google (easiest)

1. Go to [Google Cloud Console](https://console.cloud.google.com) → APIs & Services → Credentials
2. Create an OAuth 2.0 Client ID (Web application)
3. Add authorized redirect URI: `https://pubobs.example.com/auth/oidc/callback`
4. Set in `.env`:
   ```env
   PUBOBS_OIDC_ISSUER=https://accounts.google.com
   PUBOBS_OIDC_CLIENT_ID=....apps.googleusercontent.com
   PUBOBS_OIDC_CLIENT_SECRET=GOCSPX-...
   ```

### Yandex (optional second provider)

1. Go to [Yandex OAuth](https://oauth.yandex.ru) → Create app
2. Add callback: `https://pubobs.example.com/auth/yandex/callback`
3. Set `PUBOBS_YANDEX_CLIENT_ID` and `PUBOBS_YANDEX_CLIENT_SECRET` in `.env`

### Self-hosted (Authentik / Keycloak / Dex)

Set `PUBOBS_OIDC_ISSUER` to your provider's issuer URL and configure the redirect URI as above.

---

## Updating

```bash
# On local machine: rebuild frontend if changed
cd frontend && npm run build

# Sync to VPS
rsync -av --exclude=node_modules --exclude=.git --exclude=backend/data . user@your-vps:/opt/pubobs

# On VPS: rebuild and restart
cd /opt/pubobs/backend
docker compose up -d --build
```

---

## File layout on VPS

```
/opt/pubobs/
├── backend/
│   ├── Dockerfile
│   ├── docker-compose.yml
│   ├── .env                  ← your secrets (never commit this)
│   └── data/
│       ├── db/pubobs.db      ← SQLite database
│       └── repos/            ← cloned repo cache
└── frontend/
    └── src/                  ← TypeScript source (build locally)
```

---

## Troubleshooting

**Container won't start — check logs:**
```bash
docker compose logs pubobs
```

**OIDC login fails:**
- Verify the redirect URI in your provider matches exactly: `https://your-domain/auth/oidc/callback`
- Check `PUBOBS_BASE_URL` has no trailing slash

**"PUBOBS_SECRET_KEY must be 64 hex chars":**
```bash
openssl rand -hex 32   # produces exactly 64 hex characters
```

**Reset everything:**
```bash
docker compose down
rm -rf data/
docker compose up -d --build
```
