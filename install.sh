#!/usr/bin/env bash
set -euo pipefail

REPO_URL="https://github.com/rborisov/pubobs.git"
INSTALL_DIR="/opt/pubobs"
BACKEND_DIR="$INSTALL_DIR/backend"
ENV_FILE="$BACKEND_DIR/.env"
VERSION_FILE="$INSTALL_DIR/.pubobs-version"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; BOLD='\033[1m'; NC='\033[0m'

info()    { echo -e "${BLUE}==>${NC} $*"; }
success() { echo -e "${GREEN}✓${NC} $*"; }
warn()    { echo -e "${YELLOW}!${NC} $*" >&2; }
die()     { echo -e "${RED}✗${NC} $*" >&2; exit 1; }

usage() {
  echo "Usage: $0 [--update | --reinstall | --help]"
  echo "  (no flags)    Fresh install"
  echo "  --update      Pull latest code, rebuild and restart"
  echo "  --reinstall   Stop everything and reinstall from scratch"
}

MODE="install"
case "${1:-}" in
  --update)    MODE="update" ;;
  --reinstall) MODE="reinstall" ;;
  --help|-h)   usage; exit 0 ;;
  "")          ;;
  *) echo "Unknown option: $1"; usage; exit 1 ;;
esac

# ── Prerequisites ─────────────────────────────────────────────────────────────

[ "$EUID" -eq 0 ] || die "Please run as root: sudo $0 ${1:-}"

ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH_NAME="amd64" ;;
  aarch64|arm64) ARCH_NAME="arm64" ;;
  *) die "Unsupported architecture: $ARCH" ;;
esac

check_disk() {
  local free_kb
  free_kb=$(df -k / | awk 'NR==2 {print $4}')
  local free_gb=$(( free_kb / 1024 / 1024 ))
  if [ "$free_gb" -lt 5 ]; then
    warn "Low disk space: ${free_gb}GB free (5GB recommended)"
    read -rp "Continue anyway? [y/N]: " c
    [[ "$c" =~ ^[Yy]$ ]] || exit 0
  fi
}

install_git() {
  command -v git &>/dev/null && return
  info "Installing git..."
  apt-get update -qq && apt-get install -y -qq git
}

install_docker() {
  if docker info &>/dev/null && docker compose version &>/dev/null; then
    success "Docker already installed"
    return
  fi

  if ! docker info &>/dev/null; then
    info "Installing Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
  fi

  if ! docker compose version &>/dev/null; then
    info "Installing Docker Compose plugin..."
    local installed=false
    for pkg in docker-compose-plugin docker-compose-v2; do
      if apt-get install -y "$pkg" &>/dev/null 2>&1; then
        installed=true; break
      fi
    done
    if [ "$installed" = false ]; then
      info "Downloading Docker Compose from GitHub..."
      local plugin_dir="/usr/local/lib/docker/cli-plugins"
      mkdir -p "$plugin_dir"
      curl -fsSL -o "$plugin_dir/docker-compose" \
        "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-$ARCH"
      chmod +x "$plugin_dir/docker-compose"
    fi
  fi
  success "Docker ready"
}

# ── Repo management ───────────────────────────────────────────────────────────

sparse_clone() {
  local dest="$1"
  git clone --branch main --filter=blob:none --no-checkout "$REPO_URL" "$dest"
  git -C "$dest" sparse-checkout init --no-cone
  git -C "$dest" sparse-checkout set \
    '/*' \
    '!/backend/bin/' \
    "backend/bin/pubobs-linux-${ARCH_NAME}"
  git -C "$dest" checkout main
}

save_version() {
  local dir="${1:-$INSTALL_DIR}"
  if [ -d "$dir/.git" ]; then
    git -C "$dir" rev-parse HEAD > "$VERSION_FILE" 2>/dev/null || true
  fi
}

cleanup_repo() {
  info "Cleaning up source files..."
  save_version
  for path in \
    "$INSTALL_DIR/frontend" \
    "$INSTALL_DIR/docs" \
    "$INSTALL_DIR/installer" \
    "$INSTALL_DIR/obsidian-plugin" \
    "$INSTALL_DIR/.git" \
    "$INSTALL_DIR/install.sh" \
    "$INSTALL_DIR/README.md" \
    "$INSTALL_DIR/.gitattributes" \
    "$INSTALL_DIR/.gitignore" \
    "$BACKEND_DIR/cmd" \
    "$BACKEND_DIR/internal" \
    "$BACKEND_DIR/frontend" \
    "$BACKEND_DIR/go.mod" \
    "$BACKEND_DIR/go.sum" \
    "$BACKEND_DIR/Makefile" \
    "$BACKEND_DIR/server"
  do
    rm -rf "$path"
  done
  success "Cleanup done"
}

# ── Configuration ─────────────────────────────────────────────────────────────

collect_config() {
  echo ""
  echo -e "${BOLD}=== PubObs Configuration ===${NC}"
  echo ""

  read -rp "Domain (e.g. pubobs.example.com): " DOMAIN
  read -rp "Admin email: " ADMIN_EMAIL

  echo ""
  echo "OIDC provider:"
  echo "  1) Google"
  echo "  2) Yandex"
  echo "  3) Custom"
  read -rp "Choose [1-3]: " oidc_choice

  OIDC_CLIENT_ID="" OIDC_CLIENT_SECRET="" OIDC_ISSUER=""
  YANDEX_CLIENT_ID="" YANDEX_CLIENT_SECRET=""

  case "$oidc_choice" in
    1)
      OIDC_ISSUER="https://accounts.google.com"
      read -rp "Google Client ID: " OIDC_CLIENT_ID
      read -rsp "Google Client Secret: " OIDC_CLIENT_SECRET; echo
      ;;
    2)
      OIDC_ISSUER="https://login.yandex.ru"
      read -rp "Yandex Client ID: " YANDEX_CLIENT_ID
      read -rsp "Yandex Client Secret: " YANDEX_CLIENT_SECRET; echo
      ;;
    3)
      read -rp "OIDC Issuer URL: " OIDC_ISSUER
      read -rp "OIDC Client ID: " OIDC_CLIENT_ID
      read -rsp "OIDC Client Secret: " OIDC_CLIENT_SECRET; echo
      ;;
    *) die "Invalid choice" ;;
  esac

  echo ""
  read -rp "Setup nginx reverse proxy? [Y/n]: " setup_nginx
  SETUP_NGINX="${setup_nginx:-Y}"

  SETUP_TLS="n"
  if [[ "$SETUP_NGINX" =~ ^[Yy]$ ]]; then
    read -rp "Obtain TLS certificate (Let's Encrypt)? [Y/n]: " setup_tls
    SETUP_TLS="${setup_tls:-Y}"
  fi

  SECRET_KEY=$(openssl rand -hex 32 2>/dev/null \
    || tr -dc 'a-f0-9' < /dev/urandom | head -c 64)
}

write_env() {
  local use_tls="${1:-false}"
  local base_url
  if [[ "$use_tls" == "true" ]]; then
    base_url="https://$DOMAIN"
  else
    base_url="http://$DOMAIN"
  fi

  cat > "$ENV_FILE" <<EOF
PUBOBS_BASE_URL=$base_url
PUBOBS_OIDC_ISSUER=$OIDC_ISSUER
PUBOBS_OIDC_CLIENT_ID=$OIDC_CLIENT_ID
PUBOBS_OIDC_CLIENT_SECRET=$OIDC_CLIENT_SECRET
PUBOBS_SECRET_KEY=$SECRET_KEY
PUBOBS_ADMIN_EMAIL=$ADMIN_EMAIL
PUBOBS_YANDEX_CLIENT_ID=$YANDEX_CLIENT_ID
PUBOBS_YANDEX_CLIENT_SECRET=$YANDEX_CLIENT_SECRET
EOF
  chmod 600 "$ENV_FILE"
  success "Wrote $ENV_FILE"
}

# ── Docker lifecycle ──────────────────────────────────────────────────────────

build_app() {
  info "Building application..."
  docker compose -f "$BACKEND_DIR/docker-compose.yml" build
  success "Build complete"
}

start_containers() {
  info "Preparing data directories..."
  mkdir -p "$BACKEND_DIR/data/db" "$BACKEND_DIR/data/repos"
  chown -R 1000:1000 "$BACKEND_DIR/data"

  info "Starting containers..."
  docker compose -f "$BACKEND_DIR/docker-compose.yml" down 2>/dev/null || true
  docker compose -f "$BACKEND_DIR/docker-compose.yml" up -d
  wait_healthy
}

restart_containers() {
  info "Restarting containers..."
  mkdir -p "$BACKEND_DIR/data/db" "$BACKEND_DIR/data/repos"
  chown -R 1000:1000 "$BACKEND_DIR/data"
  docker compose -f "$BACKEND_DIR/docker-compose.yml" up -d
  wait_healthy
}

wait_healthy() {
  info "Waiting for app to become healthy..."
  local i=0
  while [ $i -lt 30 ]; do
    if curl -sf http://localhost:8181/healthz &>/dev/null; then
      success "App is healthy"
      return
    fi
    echo -n "."
    sleep 2
    i=$((i + 1))
  done
  echo ""
  docker compose -f "$BACKEND_DIR/docker-compose.yml" logs --tail=50 >&2
  die "App did not become healthy within 60 seconds"
}

# ── nginx / TLS ───────────────────────────────────────────────────────────────

configure_nginx() {
  info "Installing nginx..."
  apt-get install -y nginx

  cat > /etc/nginx/sites-available/pubobs <<EOF
server {
    listen 80;
    server_name $DOMAIN;
    location / {
        proxy_pass http://127.0.0.1:8181;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 120s;
        client_max_body_size 50m;
    }
}
EOF
  ln -sf /etc/nginx/sites-available/pubobs /etc/nginx/sites-enabled/pubobs
  rm -f /etc/nginx/sites-enabled/default
  nginx -t
  systemctl reload nginx
  success "nginx configured"
}

obtain_tls() {
  local server_ip
  server_ip=$(curl -s --max-time 5 https://api.ipify.org 2>/dev/null \
    || hostname -I | awk '{print $1}')
  local resolved_ip
  resolved_ip=$(getent hosts "$DOMAIN" 2>/dev/null | awk '{print $1}' || true)

  if [ -n "$resolved_ip" ] && [ -n "$server_ip" ] && [ "$resolved_ip" != "$server_ip" ]; then
    warn "DNS mismatch: $DOMAIN resolves to $resolved_ip but server IP is $server_ip"
    read -rp "Continue anyway? [y/N]: " c
    [[ "$c" =~ ^[Yy]$ ]] || return 1
  fi

  info "Obtaining TLS certificate for $DOMAIN..."
  apt-get install -y certbot python3-certbot-nginx
  certbot --nginx -d "$DOMAIN" \
    --non-interactive --agree-tos \
    --register-unsafely-without-email

  write_env "true"
  docker compose -f "$BACKEND_DIR/docker-compose.yml" restart
  success "TLS configured"
}

# ── Modes ─────────────────────────────────────────────────────────────────────

do_install() {
  echo -e "${BOLD}=== PubObs Installer ===${NC}"
  echo ""

  if [ -f "$ENV_FILE" ]; then
    die "PubObs is already installed at $INSTALL_DIR. Use --update or --reinstall."
  fi

  check_disk
  install_git

  info "Cloning PubObs to $INSTALL_DIR..."
  sparse_clone "$INSTALL_DIR"

  collect_config
  install_docker
  write_env "false"
  build_app
  start_containers
  cleanup_repo

  if [[ "$SETUP_NGINX" =~ ^[Yy]$ ]]; then
    configure_nginx
    if [[ "$SETUP_TLS" =~ ^[Yy]$ ]]; then
      obtain_tls || warn "TLS setup failed — running without HTTPS"
    fi
  fi

  echo ""
  echo -e "${GREEN}${BOLD}=== Installation complete! ===${NC}"
  if [[ "$SETUP_NGINX" =~ ^[Yy]$ ]] && [[ "$SETUP_TLS" =~ ^[Yy]$ ]]; then
    echo "  URL: https://$DOMAIN"
  elif [[ "$SETUP_NGINX" =~ ^[Yy]$ ]]; then
    echo "  URL: http://$DOMAIN"
  else
    local ip
    ip=$(curl -s --max-time 5 https://api.ipify.org 2>/dev/null \
      || hostname -I | awk '{print $1}')
    echo "  URL: http://$ip:8181"
  fi
  echo ""
}

do_update() {
  echo -e "${BOLD}=== PubObs Update ===${NC}"
  echo ""

  [ -f "$ENV_FILE" ] || die "PubObs is not installed at $INSTALL_DIR. Run without flags to install."

  local current_ver="(unknown)"
  [ -f "$VERSION_FILE" ] && current_ver=$(cat "$VERSION_FILE")

  local tmp_dir
  tmp_dir=$(mktemp -d)
  trap 'rm -rf "$tmp_dir"' EXIT

  info "Fetching latest code..."
  sparse_clone "$tmp_dir"

  local new_ver
  new_ver=$(git -C "$tmp_dir" rev-parse HEAD 2>/dev/null || echo "unknown")

  if [ "$current_ver" = "$new_ver" ]; then
    success "Already up to date ($new_ver)"
    exit 0
  fi

  info "Updating: ${current_ver:0:8} → ${new_ver:0:8}"

  # Copy updated files
  cp -f "$tmp_dir/backend/docker-compose.yml" "$BACKEND_DIR/"
  [ -f "$tmp_dir/backend/Dockerfile" ] && \
    cp -f "$tmp_dir/backend/Dockerfile" "$BACKEND_DIR/"
  local new_bin="$tmp_dir/backend/bin/pubobs-linux-${ARCH_NAME}"
  if [ -f "$new_bin" ]; then
    mkdir -p "$BACKEND_DIR/bin"
    cp -f "$new_bin" "$BACKEND_DIR/bin/pubobs-linux-${ARCH_NAME}"
    chmod +x "$BACKEND_DIR/bin/pubobs-linux-${ARCH_NAME}"
  fi

  echo "$new_ver" > "$VERSION_FILE"
  trap - EXIT
  rm -rf "$tmp_dir"

  build_app
  restart_containers
  success "Updated to ${new_ver:0:8}"
}

do_reinstall() {
  echo -e "${BOLD}=== PubObs Reinstall ===${NC}"
  echo ""
  warn "This will stop all containers and remove the current installation."

  read -rp "Preserve data (database and repositories)? [Y/n]: " preserve
  PRESERVE_DATA="${preserve:-Y}"

  local backup_dir=""
  if [[ ! "$PRESERVE_DATA" =~ ^[Yy]$ ]]; then
    warn "ALL DATA WILL BE PERMANENTLY DELETED."
    read -rp "Type 'yes' to confirm: " confirm
    [ "$confirm" = "yes" ] || { info "Aborted."; exit 0; }
  elif [ -d "$BACKEND_DIR/data" ]; then
    backup_dir="/tmp/pubobs-data-backup-$(date +%Y%m%d-%H%M%S)"
    info "Backing up data to $backup_dir..."
    cp -r "$BACKEND_DIR/data" "$backup_dir"
    success "Data backed up to $backup_dir"
  fi

  if [ -f "$BACKEND_DIR/docker-compose.yml" ]; then
    info "Stopping containers..."
    docker compose -f "$BACKEND_DIR/docker-compose.yml" down || true
  fi

  rm -rf "$INSTALL_DIR"

  do_install

  if [ -n "$backup_dir" ] && [ -d "$backup_dir" ]; then
    info "Restoring data..."
    mkdir -p "$BACKEND_DIR/data"
    cp -r "$backup_dir/." "$BACKEND_DIR/data/"
    chown -R 1000:1000 "$BACKEND_DIR/data"
    docker compose -f "$BACKEND_DIR/docker-compose.yml" restart
    wait_healthy
    success "Data restored from $backup_dir"
  fi
}

# ── Entry point ───────────────────────────────────────────────────────────────

case "$MODE" in
  install)   do_install ;;
  update)    do_update ;;
  reinstall) do_reinstall ;;
esac
