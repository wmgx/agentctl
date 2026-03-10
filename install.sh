#!/usr/bin/env bash
set -euo pipefail

REPO="wmgx/agentctl"
INSTALL_DIR="${AGENTCTL_INSTALL_DIR:-$HOME/.agentctl}"
BIN_DIR="${AGENTCTL_BIN_DIR:-/usr/local/bin}"
BINARY="agentctl"

# ── helpers ──────────────────────────────────────────────────────────────────

info()    { echo "[agentctl] $*"; }
success() { echo "[agentctl] ✓ $*"; }
error()   { echo "[agentctl] ✗ $*" >&2; exit 1; }

need() {
  command -v "$1" &>/dev/null || error "Required tool not found: $1"
}

# ── check dependencies ────────────────────────────────────────────────────────

check_deps() {
  need git
  need go
  need tmux
}

# ── install ───────────────────────────────────────────────────────────────────

do_install() {
  check_deps

  info "Installing agentctl to $INSTALL_DIR ..."

  if [ -d "$INSTALL_DIR" ]; then
    info "Directory $INSTALL_DIR already exists, pulling latest changes ..."
    git -C "$INSTALL_DIR" fetch --tags --prune
    LATEST=$(git -C "$INSTALL_DIR" tag --sort=-v:refname | head -1)
    if [ -n "$LATEST" ]; then
      git -C "$INSTALL_DIR" checkout "$LATEST"
    else
      git -C "$INSTALL_DIR" pull
    fi
  else
    git clone "https://github.com/$REPO.git" "$INSTALL_DIR"
  fi

  info "Building ..."
  (cd "$INSTALL_DIR" && go build -o "$BINARY" ./cmd/server)

  info "Installing binary to $BIN_DIR/$BINARY ..."
  install -m 755 "$INSTALL_DIR/$BINARY" "$BIN_DIR/$BINARY"

  if [ ! -f "$INSTALL_DIR/config.json" ]; then
    cp "$INSTALL_DIR/config.example.json" "$INSTALL_DIR/config.json"
    info "Created config at $INSTALL_DIR/config.json — please fill in your credentials."
  fi

  success "agentctl installed! Run: agentctl --help"
}

# ── update ────────────────────────────────────────────────────────────────────

do_update() {
  check_deps

  [ -d "$INSTALL_DIR" ] || error "agentctl is not installed at $INSTALL_DIR. Run install first."

  info "Fetching latest version ..."
  git -C "$INSTALL_DIR" fetch --tags --prune

  CURRENT=$(git -C "$INSTALL_DIR" describe --tags --abbrev=0 2>/dev/null || echo "unknown")
  LATEST=$(git -C "$INSTALL_DIR" tag --sort=-v:refname | head -1)

  if [ "$CURRENT" = "$LATEST" ]; then
    info "Already on latest version: $CURRENT"
    exit 0
  fi

  info "Upgrading $CURRENT → $LATEST ..."
  git -C "$INSTALL_DIR" checkout "$LATEST"

  info "Rebuilding ..."
  (cd "$INSTALL_DIR" && go build -o "$BINARY" ./cmd/server)
  install -m 755 "$INSTALL_DIR/$BINARY" "$BIN_DIR/$BINARY"

  success "Updated to $LATEST"
}

# ── uninstall ─────────────────────────────────────────────────────────────────

do_uninstall() {
  info "Removing $BIN_DIR/$BINARY ..."
  rm -f "$BIN_DIR/$BINARY"
  info "Removing $INSTALL_DIR ..."
  rm -rf "$INSTALL_DIR"
  success "agentctl uninstalled."
}

# ── entrypoint ────────────────────────────────────────────────────────────────

CMD="${1:-install}"

case "$CMD" in
  install)   do_install ;;
  update)    do_update ;;
  uninstall) do_uninstall ;;
  *)         error "Unknown command: $CMD. Usage: install.sh [install|update|uninstall]" ;;
esac
