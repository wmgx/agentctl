#!/usr/bin/env bash
set -euo pipefail

REPO="wmgx/agentctl"
INSTALL_DIR="${AGENTCTL_INSTALL_DIR:-$HOME/.agentctl}"
BIN_DIR="${AGENTCTL_BIN_DIR:-/usr/local/bin}"
BINARY="agentctl"
SERVICE_NAME="agentctl"

# ── helpers ──────────────────────────────────────────────────────────────────

info()    { echo "[agentctl] $*"; }
success() { echo "[agentctl] ✓ $*"; }
warn()    { echo "[agentctl] ! $*"; }
error()   { echo "[agentctl] ✗ $*" >&2; exit 1; }

need() {
  command -v "$1" &>/dev/null || error "Required tool not found: $1"
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "macos" ;;
    Linux)  echo "linux" ;;
    MINGW*|MSYS*|CYGWIN*|Windows_NT)
      echo ""
      echo "  Windows is not currently supported."
      echo "  Please use WSL2 (Ubuntu) as a workaround:"
      echo "  https://learn.microsoft.com/en-us/windows/wsl/install"
      echo ""
      exit 1
      ;;
    *) error "Unsupported OS: $(uname -s)" ;;
  esac
}

# ── check dependencies ────────────────────────────────────────────────────────

check_deps() {
  need git
  need go
  need tmux
}

# ── service helpers ───────────────────────────────────────────────────────────

# macOS: launchd plist
PLIST_PATH="$HOME/Library/LaunchAgents/com.wmgx.$SERVICE_NAME.plist"

write_plist() {
  mkdir -p "$HOME/Library/LaunchAgents"
  cat > "$PLIST_PATH" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.wmgx.$SERVICE_NAME</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_DIR/$BINARY</string>
    <string>--config</string>
    <string>$INSTALL_DIR/config.json</string>
  </array>
  <key>WorkingDirectory</key>
  <string>$INSTALL_DIR</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$INSTALL_DIR/log/stdout.log</string>
  <key>StandardErrorPath</key>
  <string>$INSTALL_DIR/log/stderr.log</string>
</dict>
</plist>
EOF
  mkdir -p "$INSTALL_DIR/log"
}

register_macos() {
  write_plist
  launchctl unload "$PLIST_PATH" 2>/dev/null || true
  launchctl load -w "$PLIST_PATH"
  success "Service registered via launchd (starts on login)"
  info "Logs: $INSTALL_DIR/log/"
}

unregister_macos() {
  if [ -f "$PLIST_PATH" ]; then
    launchctl unload "$PLIST_PATH" 2>/dev/null || true
    rm -f "$PLIST_PATH"
    success "launchd service removed"
  fi
}

reload_macos() {
  write_plist
  launchctl unload "$PLIST_PATH" 2>/dev/null || true
  launchctl load -w "$PLIST_PATH"
  success "Service reloaded"
}

# Linux: systemd user service
SYSTEMD_DIR="$HOME/.config/systemd/user"
SERVICE_FILE="$SYSTEMD_DIR/$SERVICE_NAME.service"

write_service() {
  mkdir -p "$SYSTEMD_DIR"
  cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=agentctl — AI CLI to messaging channel bridge
After=network.target

[Service]
Type=simple
ExecStart=$BIN_DIR/$BINARY --config $INSTALL_DIR/config.json
WorkingDirectory=$INSTALL_DIR
Restart=on-failure
RestartSec=5
StandardOutput=append:$INSTALL_DIR/log/stdout.log
StandardError=append:$INSTALL_DIR/log/stderr.log

[Install]
WantedBy=default.target
EOF
  mkdir -p "$INSTALL_DIR/log"
}

register_linux() {
  write_service
  systemctl --user daemon-reload
  systemctl --user enable --now "$SERVICE_NAME"
  success "Service registered via systemd --user (starts on login)"
  info "Status: systemctl --user status $SERVICE_NAME"
  info "Logs:   $INSTALL_DIR/log/"
}

unregister_linux() {
  if systemctl --user is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    systemctl --user stop "$SERVICE_NAME"
  fi
  systemctl --user disable "$SERVICE_NAME" 2>/dev/null || true
  rm -f "$SERVICE_FILE"
  systemctl --user daemon-reload
  success "systemd service removed"
}

reload_linux() {
  write_service
  systemctl --user daemon-reload
  systemctl --user restart "$SERVICE_NAME"
  success "Service restarted"
}

register_service() {
  local os="$1"
  case "$os" in
    macos) register_macos ;;
    linux) register_linux ;;
  esac
}

unregister_service() {
  local os="$1"
  case "$os" in
    macos) unregister_macos ;;
    linux) unregister_linux ;;
  esac
}

reload_service() {
  local os="$1"
  case "$os" in
    macos) reload_macos ;;
    linux) reload_linux ;;
  esac
}

# ── install ───────────────────────────────────────────────────────────────────

do_install() {
  local os
  os=$(detect_os)
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
    warn "Config created at $INSTALL_DIR/config.json — fill in your credentials before starting."
  fi

  register_service "$os"

  success "agentctl installed!"
}

# ── update ────────────────────────────────────────────────────────────────────

do_update() {
  local os
  os=$(detect_os)
  check_deps

  [ -d "$INSTALL_DIR" ] || error "agentctl is not installed at $INSTALL_DIR. Run install first."

  info "Fetching latest version ..."
  git -C "$INSTALL_DIR" fetch --tags --prune

  CURRENT=$(git -C "$INSTALL_DIR" describe --tags --abbrev=0 2>/dev/null || echo "unknown")
  LATEST=$(git -C "$INSTALL_DIR" tag --sort=-v:refname | head -1)

  if [ -z "$LATEST" ]; then
    info "No tagged releases found, pulling latest commit ..."
    git -C "$INSTALL_DIR" pull
  elif [ "$CURRENT" = "$LATEST" ]; then
    success "Already on latest version: $CURRENT"
    exit 0
  else
    info "Upgrading $CURRENT → $LATEST ..."
    git -C "$INSTALL_DIR" checkout "$LATEST"
  fi

  info "Rebuilding ..."
  (cd "$INSTALL_DIR" && go build -o "$BINARY" ./cmd/server)
  install -m 755 "$INSTALL_DIR/$BINARY" "$BIN_DIR/$BINARY"

  reload_service "$os"

  success "Updated to ${LATEST:-latest}"
}

# ── uninstall ─────────────────────────────────────────────────────────────────

do_uninstall() {
  local os
  os=$(detect_os)

  unregister_service "$os"

  info "Removing binary $BIN_DIR/$BINARY ..."
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
  *) error "Unknown command: $CMD. Usage: install.sh [install|update|uninstall]" ;;
esac
