#!/usr/bin/env bash
# Copyright (c) 2026 VGS https://vgst.net
# SPDX-License-Identifier: MIT
#
# Proxmox Autoscaler — deployment script
# Usage:
#   ./deploy.sh            Install or update to the latest release
#   ./deploy.sh v1.2.0     Install or update to a specific version
#   ./deploy.sh --uninstall

set -euo pipefail

# ─── Configuration ────────────────────────────────────────────────────────────

GITHUB_REPO="vgstdev/proxmox-autoscaler"
BINARY_NAME="proxmox-autoscaler"
SERVICE_NAME="proxmox-autoscaler"

INSTALL_BIN="/usr/local/bin/${BINARY_NAME}"
CONFIG_DIR="/etc/proxmox-autoscaler"
CONFIG_FILE="${CONFIG_DIR}/autoscaler.yaml"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
LOG_DIR="/var/log/proxmox-autoscaler"
DB_DIR="/var/lib/proxmox-autoscaler"

# ─── Colors ───────────────────────────────────────────────────────────────────

if [[ -t 1 ]]; then
  RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
  BLUE='\033[0;34m'; BOLD='\033[1m'; RESET='\033[0m'
else
  RED=''; GREEN=''; YELLOW=''; BLUE=''; BOLD=''; RESET=''
fi

info()    { echo -e "${BLUE}[INFO]${RESET}  $*"; }
success() { echo -e "${GREEN}[OK]${RESET}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${RESET}  $*"; }
error()   { echo -e "${RED}[ERROR]${RESET} $*" >&2; exit 1; }
step()    { echo -e "\n${BOLD}▸ $*${RESET}"; }

# ─── Helpers ──────────────────────────────────────────────────────────────────

check_root() {
  [[ $EUID -eq 0 ]] || error "This script must be run as root (sudo $0)"
}

detect_arch() {
  local machine
  machine="$(uname -m)"
  case "$machine" in
    x86_64)  echo "amd64" ;;
    aarch64) echo "arm64" ;;
    *) error "Unsupported architecture: $machine" ;;
  esac
}

check_deps() {
  local missing=()
  for cmd in curl tar systemctl; do
    command -v "$cmd" &>/dev/null || missing+=("$cmd")
  done
  [[ ${#missing[@]} -eq 0 ]] || error "Missing required commands: ${missing[*]}"
}

service_is_active() {
  systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null
}

service_is_enabled() {
  systemctl is-enabled --quiet "${SERVICE_NAME}" 2>/dev/null
}

get_installed_version() {
  if [[ -x "$INSTALL_BIN" ]]; then
    "$INSTALL_BIN" --version 2>/dev/null | awk '{print $NF}' || echo "unknown"
  else
    echo "none"
  fi
}

get_latest_version() {
  local version
  version="$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  [[ -n "$version" ]] || error "Could not fetch latest version from GitHub. Check the GITHUB_REPO variable."
  echo "$version"
}

download_and_extract() {
  local version="$1"
  local arch="$2"
  local archive="${BINARY_NAME}_${version#v}_linux_${arch}.tar.gz"
  local url="https://github.com/${GITHUB_REPO}/releases/download/${version}/${archive}"
  local tmpdir
  tmpdir="$(mktemp -d)"

  info "Downloading ${archive}..." >&2
  curl -fsSL --progress-bar "$url" -o "${tmpdir}/${archive}" \
    || error "Download failed. Is version ${version} available? URL: ${url}"

  info "Extracting..." >&2
  tar -xzf "${tmpdir}/${archive}" -C "$tmpdir"

  echo "$tmpdir"
}

# ─── Install ──────────────────────────────────────────────────────────────────

do_install() {
  local target_version="${1:-}"
  local arch
  arch="$(detect_arch)"

  step "Checking versions"
  local installed_version
  installed_version="$(get_installed_version)"

  if [[ -z "$target_version" ]]; then
    info "Fetching latest version from GitHub..."
    target_version="$(get_latest_version)"
  fi

  info "Installed version : ${installed_version}"
  info "Target version    : ${target_version}"

  if [[ "$installed_version" == "${target_version#v}" ]]; then
    warn "Version ${target_version} is already installed. Use --force to reinstall."
    exit 0
  fi

  local is_update=false
  [[ "$installed_version" != "none" ]] && is_update=true

  # ── Download ────────────────────────────────────────────────────────────────
  step "Downloading release"
  local tmpdir
  tmpdir="$(download_and_extract "$target_version" "$arch")"
  # shellcheck disable=SC2064
  trap "rm -rf $tmpdir" EXIT

  # ── Stop service if running ─────────────────────────────────────────────────
  if $is_update && service_is_active; then
    step "Stopping service"
    systemctl stop "${SERVICE_NAME}"
    success "Service stopped (active boosts will be reverted by the service on shutdown)"
  fi

  # ── Binary ──────────────────────────────────────────────────────────────────
  step "Installing binary"
  install -m 0755 "${tmpdir}/${BINARY_NAME}" "$INSTALL_BIN"
  success "Binary installed at ${INSTALL_BIN}"

  # ── Directories ─────────────────────────────────────────────────────────────
  step "Creating directories"
  install -d -m 0755 "$CONFIG_DIR"
  install -d -m 0755 "$LOG_DIR"
  install -d -m 0750 "$DB_DIR"
  success "Created: ${CONFIG_DIR}, ${LOG_DIR}, ${DB_DIR}"

  # ── Config (never overwrite existing) ───────────────────────────────────────
  step "Installing config"
  if [[ -f "$CONFIG_FILE" ]]; then
    warn "Config already exists at ${CONFIG_FILE} — not overwritten"
    info  "New example config saved at ${CONFIG_FILE}.new — diff and merge manually if needed"
    install -m 0640 "${tmpdir}/autoscaler.yaml" "${CONFIG_FILE}.new"
  else
    install -m 0640 "${tmpdir}/autoscaler.yaml" "$CONFIG_FILE"
    success "Config installed at ${CONFIG_FILE}"
    warn "Edit ${CONFIG_FILE} with your Proxmox credentials before starting the service"
  fi

  # ── Systemd service ─────────────────────────────────────────────────────────
  step "Installing systemd service"
  install -m 0644 "${tmpdir}/proxmox-autoscaler.service" "$SERVICE_FILE"
  systemctl daemon-reload
  success "Service unit installed at ${SERVICE_FILE}"

  # ── Enable & start ──────────────────────────────────────────────────────────
  step "Enabling service"
  systemctl enable "${SERVICE_NAME}" --quiet
  success "Service enabled (will start on boot)"

  if [[ -f "$CONFIG_FILE" ]] && ! grep -q "xxxx" "$CONFIG_FILE" 2>/dev/null; then
    step "Starting service"
    systemctl start "${SERVICE_NAME}"
    sleep 2
    if service_is_active; then
      success "Service is running"
    else
      warn "Service failed to start. Check logs: journalctl -u ${SERVICE_NAME} -n 50"
    fi
  else
    warn "Service not started — edit ${CONFIG_FILE} first, then run: systemctl start ${SERVICE_NAME}"
  fi

  # ── Summary ─────────────────────────────────────────────────────────────────
  echo
  echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  if $is_update; then
    echo -e "${GREEN}${BOLD}  Proxmox Autoscaler updated to ${target_version}${RESET}"
  else
    echo -e "${GREEN}${BOLD}  Proxmox Autoscaler ${target_version} installed${RESET}"
  fi
  echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo
  echo "  Config   : ${CONFIG_FILE}"
  echo "  Logs     : journalctl -u ${SERVICE_NAME} -f"
  echo "  Status   : systemctl status ${SERVICE_NAME}"
  echo "  DB       : ${DB_DIR}/state.db"
  echo
}

# ─── Uninstall ────────────────────────────────────────────────────────────────

do_uninstall() {
  step "Uninstalling Proxmox Autoscaler"

  if service_is_active; then
    info "Stopping service (active boosts will be reverted)..."
    systemctl stop "${SERVICE_NAME}"
    success "Service stopped"
  fi

  if service_is_enabled; then
    systemctl disable "${SERVICE_NAME}" --quiet
    success "Service disabled"
  fi

  [[ -f "$SERVICE_FILE" ]] && { rm -f "$SERVICE_FILE"; systemctl daemon-reload; success "Service unit removed"; }
  [[ -f "$INSTALL_BIN" ]]  && { rm -f "$INSTALL_BIN";  success "Binary removed"; }

  echo
  warn "The following directories were NOT removed (may contain your config and state):"
  echo "  ${CONFIG_DIR}"
  echo "  ${LOG_DIR}"
  echo "  ${DB_DIR}"
  echo
  read -r -p "Remove them too? This will delete config and DB state [y/N] " confirm
  if [[ "${confirm,,}" == "y" ]]; then
    rm -rf "$CONFIG_DIR" "$LOG_DIR" "$DB_DIR"
    success "Directories removed"
  else
    info "Directories kept"
  fi

  success "Proxmox Autoscaler uninstalled"
}

# ─── Entry point ──────────────────────────────────────────────────────────────

main() {
  echo -e "${BOLD}"
  echo "  ╔═══════════════════════════════════════╗"
  echo "  ║      Proxmox Autoscaler Installer     ║"
  echo "  ║      https://vgst.net                 ║"
  echo "  ╚═══════════════════════════════════════╝"
  echo -e "${RESET}"

  check_root
  check_deps

  case "${1:-}" in
    --uninstall) do_uninstall ;;
    --force)
      # Remove installed version marker so the version check is skipped
      [[ -f "$INSTALL_BIN" ]] && rm -f "$INSTALL_BIN"
      do_install "${2:-}"
      ;;
    --help|-h)
      echo "Usage:"
      echo "  $0                   Install or update to the latest release"
      echo "  $0 v1.2.0            Install or update to a specific version"
      echo "  $0 --force           Reinstall current version"
      echo "  $0 --uninstall       Remove the service and binary"
      ;;
    *)
      do_install "${1:-}"
      ;;
  esac
}

main "$@"
