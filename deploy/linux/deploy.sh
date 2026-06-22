#!/usr/bin/env bash
# deploy/linux/deploy.sh — Deploy patches-endpoint-server to every host in inventory.csv.
#
# Usage:
#   deploy.sh <inventory.csv> <binary> <service-file> <config-file>
#
# Optional environment variables:
#   SSH_USER   Login username on remote hosts (default: current user)
#   SSH_PORT   SSH port (default: 22)
#   SSH_KEY    Path to a private key (-i flag)

set -uo pipefail

# ---------------------------------------------------------------------------
# Args
# ---------------------------------------------------------------------------
if [[ $# -lt 4 ]]; then
    echo "Usage: $0 <inventory.csv> <binary> <service-file> <config-file>" >&2
    exit 1
fi

INVENTORY_CSV="$1"
BINARY="$2"
SERVICE_FILE="$3"
CONFIG_FILE="$4"

SSH_USER="${SSH_USER:-$USER}"
SSH_PORT="${SSH_PORT:-22}"

for f in "$INVENTORY_CSV" "$BINARY" "$SERVICE_FILE"; do
    if [[ ! -f "$f" ]]; then
        echo "ERROR: required file not found: $f" >&2
        exit 1
    fi
done

# ---------------------------------------------------------------------------
# Parse inventory first so we can show the host list before prompting
# ---------------------------------------------------------------------------
if [[ ! -f "$INVENTORY_CSV" ]]; then
    echo "ERROR: inventory file not found: $INVENTORY_CSV" >&2
    exit 1
fi

HOSTS=()
HOST_USERS=()
while IFS='|' read -r host os_type; do
    [[ -n "$host" ]] || continue
    case "$os_type" in
        rhel)   user="root" ;;
        ubuntu) user="lroland" ;;
        *)      user="${SSH_USER}" ;;
    esac
    HOSTS+=("$host")
    HOST_USERS+=("$user")
done < <(awk -F',' 'NR>1 {
    gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", $2)
    gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", $4)
    if ($2 != "") print $2 "|" $4
}' "$INVENTORY_CSV")

if [[ ${#HOSTS[@]} -eq 0 ]]; then
    echo "ERROR: no hosts found in $INVENTORY_CSV" >&2
    exit 1
fi

echo "Deploying patches-endpoint-server to ${#HOSTS[@]} host(s):"
for i in "${!HOSTS[@]}"; do
    echo "  ${HOST_USERS[$i]}@${HOSTS[$i]}"
done
echo ""

# ---------------------------------------------------------------------------
# SSH / sshpass setup
# ---------------------------------------------------------------------------

# ControlMaster keeps one authenticated connection open per host so that the
# multiple SSH/SCP steps inside deploy_host never re-prompt for a password.
CTRL_DIR=$(mktemp -d /tmp/agent_patches_ssh.XXXXXX)

SSH_BASE_OPTS=(
    -o StrictHostKeyChecking=accept-new
    -o ConnectTimeout=10
    -o BatchMode=no
    -o ControlMaster=auto
    -o "ControlPath=$CTRL_DIR/%h-%p-%r"
    -o ControlPersist=120s
    -p "$SSH_PORT"
)
[[ -n "${SSH_KEY:-}" ]] && SSH_BASE_OPTS+=(-i "$SSH_KEY")

DEPLOY_PASS=""

if command -v sshpass &>/dev/null; then
    read -rsp "Deploy password (SSH + sudo, Enter for key-based auth): " DEPLOY_PASS
    echo
    export SSHPASS="$DEPLOY_PASS"
    SSH_CMD=(sshpass -e ssh)
    SCP_CMD=(sshpass -e scp)
else
    echo "Note: sshpass not found — using key-based SSH auth."
    if [[ "$SSH_USER" != "root" ]]; then
        read -rsp "Sudo password on remote hosts (Enter if passwordless sudo): " DEPLOY_PASS
        echo
    fi
    SSH_CMD=(ssh)
    SCP_CMD=(scp)
fi

# -O forces the legacy SCP protocol (not SFTP) so the SFTP subsystem's
# SELinux context cannot block writes to /tmp on RHEL hosts.
# ControlPath reuses the already-authenticated ControlMaster socket so SCP
# never needs to re-authenticate.
SCP_OPTS=(
    -O
    -P "$SSH_PORT"
    -o StrictHostKeyChecking=accept-new
    -o ControlMaster=auto
    -o "ControlPath=$CTRL_DIR/%h-%p-%r"
    -o ControlPersist=120s
)
[[ -n "${SSH_KEY:-}" ]] && SCP_OPTS+=(-i "$SSH_KEY")

# ---------------------------------------------------------------------------
# Remote setup script (written to a local tempfile, scp'd and executed)
# ---------------------------------------------------------------------------
SETUP_SCRIPT=$(mktemp /tmp/agent_patches_setup.XXXXXX.sh)
trap 'rm -f "$SETUP_SCRIPT"; rm -rf "$CTRL_DIR"' EXIT

cat > "$SETUP_SCRIPT" << 'REMOTE_SCRIPT'
#!/bin/bash
set -uo pipefail
INSTALL_ROOT=/opt/agent_patches
SERVICE_USER=agent_patches

step() { echo "  → $*"; }
warn() { echo "  ⚠ WARNING: $*"; }
ok()   { echo "    ✓ $*"; }

# ── smartmontools (best-effort) ──────────────────────────────────────────────
step "Checking smartmontools..."
if command -v smartctl &>/dev/null; then
    ok "already installed ($(smartctl --version | head -1))"
else
    step "Installing smartmontools..."
    if command -v apt-get &>/dev/null; then
        apt-get install -y -qq smartmontools 2>&1 && ok "installed via apt-get" \
            || warn "smartmontools install failed (apt-get) — continuing without it"
    elif command -v dnf &>/dev/null; then
        dnf install -y -q smartmontools 2>&1 && ok "installed via dnf" \
            || warn "smartmontools install failed (dnf) — continuing without it"
    elif command -v yum &>/dev/null; then
        yum install -y -q smartmontools 2>&1 && ok "installed via yum" \
            || warn "smartmontools install failed (yum) — continuing without it"
    else
        warn "no supported package manager found — skipping smartmontools"
    fi
fi

# ── System user ──────────────────────────────────────────────────────────────
step "Checking service user '$SERVICE_USER'..."
if id "$SERVICE_USER" &>/dev/null; then
    ok "user already exists"
else
    if [[ -f /usr/sbin/nologin ]]; then NOLOGIN=/usr/sbin/nologin
    else                                 NOLOGIN=/sbin/nologin
    fi
    if getent group sudo &>/dev/null; then SUDO_GROUP=sudo
    else                                    SUDO_GROUP=wheel
    fi
    useradd --system --no-create-home \
            --shell "$NOLOGIN" \
            --groups "$SUDO_GROUP" \
            "$SERVICE_USER"
    ok "created (shell: $NOLOGIN, group: $SUDO_GROUP)"
fi

# ── Directory structure ───────────────────────────────────────────────────────
step "Creating directory structure under $INSTALL_ROOT..."
mkdir -p "$INSTALL_ROOT"/{bin,config,data,logs}
chmod 750 "$INSTALL_ROOT"
chmod 755 "$INSTALL_ROOT/bin"
chmod 750 "$INSTALL_ROOT"/{config,data,logs}
chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_ROOT"
ok "directories ready"

# ── Stop running instance first (before replacing binary) ─────────────────────
step "Stopping any running instance..."
if pkill -TERM -f patches-endpoint-server 2>/dev/null; then
    ok "sent SIGTERM to running process"
    sleep 2
    pkill -KILL -f patches-endpoint-server 2>/dev/null && ok "sent SIGKILL to remaining process" || true
else
    ok "no running process found"
fi

# ── Binary ───────────────────────────────────────────────────────────────────
step "Installing binary..."
install -o "$SERVICE_USER" -g "$SERVICE_USER" -m 755 \
    /tmp/patches-endpoint-server "$INSTALL_ROOT/bin/patches-endpoint-server"
ok "installed to $INSTALL_ROOT/bin/patches-endpoint-server"

# ── Systemd unit ─────────────────────────────────────────────────────────────
step "Installing systemd unit..."
install -o root -g root -m 644 \
    /tmp/agent_patches.service /etc/systemd/system/agent_patches.service
ok "unit file installed"
step "Reloading systemd daemon..."
systemctl daemon-reload
ok "daemon reloaded"

# ── Config ────────────────────────────────────────────────────────────────────
step "Deploying config..."
if [[ -f /tmp/endpoint-server-config.yaml ]]; then
    install -o "$SERVICE_USER" -g "$SERVICE_USER" -m 640 \
        /tmp/endpoint-server-config.yaml "$INSTALL_ROOT/config/config.yaml"
    ok "config written to $INSTALL_ROOT/config/config.yaml"
else
    warn "no config file found in /tmp — skipping (existing config preserved)"
fi

# ── Start service ─────────────────────────────────────────────────────────────
step "Enabling and starting agent_patches service..."
systemctl enable --now agent_patches
systemctl restart agent_patches
ok "service is $(systemctl is-active agent_patches)"

# ── Cleanup temp files ────────────────────────────────────────────────────────
step "Cleaning up temporary files..."
rm -f /tmp/patches-endpoint-server \
      /tmp/agent_patches.service \
      /tmp/endpoint-server-config.yaml \
      /tmp/agent_patches_setup.*.sh
ok "done"
REMOTE_SCRIPT

# ---------------------------------------------------------------------------
# Per-host deploy
# ---------------------------------------------------------------------------
deploy_host() {
    local host=$1
    local host_user=$2
    echo ""
    echo "┌─ $host"

    # Copy binary, service file, and setup script
    local files=("$BINARY" "$SERVICE_FILE" "$SETUP_SCRIPT")
    if [[ -f "$CONFIG_FILE" ]]; then
        files+=("$CONFIG_FILE")
    else
        echo "│  ⚠ WARNING: config file not found ($CONFIG_FILE) — skipping config deploy"
    fi

    echo "│  Connecting as ${host_user}@${host}"
    if ! "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" true 2>&1 | sed 's/^/│  /'; then
        echo "│  ✗ FAILED: could not connect to $host"
        return 1
    fi

    echo "│  Copying files..."
    if ! "${SCP_CMD[@]}" "${SCP_OPTS[@]}" "${files[@]}" "${host_user}@${host}:/tmp/" 2>&1 | sed 's/^/│  /'; then
        echo "│  ✗ FAILED: could not copy files to $host"
        return 1
    fi

    # Rename the config file to the expected name on the remote
    if [[ -f "$CONFIG_FILE" ]]; then
        local cfg_base
        cfg_base=$(basename "$CONFIG_FILE")
        if [[ "$cfg_base" != "endpoint-server-config.yaml" ]]; then
            "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
                "mv /tmp/$cfg_base /tmp/endpoint-server-config.yaml" 2>/dev/null || true
        fi
    fi

    # Rename the setup script to its expected name
    local setup_base
    setup_base=$(basename "$SETUP_SCRIPT")
    "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
        "cp /tmp/$setup_base /tmp/agent_patches_setup.$$.sh" 2>/dev/null || true

    echo "│  Running setup..."
    local rc=0
    if [[ "$host_user" == "root" ]]; then
        # RHEL: logging in as root, no sudo needed
        "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "bash /tmp/agent_patches_setup.$$.sh" 2>&1 | sed 's/^/│  /' || rc=$?
    elif [[ -n "$DEPLOY_PASS" ]]; then
        # Ubuntu: non-root login, feed password to sudo -S
        "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "sudo -S -p '' bash /tmp/agent_patches_setup.$$.sh" \
            2>&1 <<< "$DEPLOY_PASS" | sed 's/^/│  /' || rc=$?
    else
        # Passwordless sudo
        "${SSH_CMD[@]}" -tt "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "sudo bash /tmp/agent_patches_setup.$$.sh" 2>&1 | sed 's/^/│  /' || rc=$?
    fi

    if [[ $rc -eq 0 ]]; then
        echo "└─ ✓ $host — done"
    else
        echo "└─ ✗ $host — FAILED (exit $rc)"
    fi

    # Close the ControlMaster socket for this host now that we're done with it.
    "${SSH_CMD[@]}" -O exit -o "ControlPath=$CTRL_DIR/%h-%p-%r" \
        "${host_user}@${host}" 2>/dev/null || true

    return $rc
}

# ---------------------------------------------------------------------------
# Main — iterate inventory
# ---------------------------------------------------------------------------
echo "  binary    : $BINARY"
echo ""

FAILED=0
for i in "${!HOSTS[@]}"; do
    deploy_host "${HOSTS[$i]}" "${HOST_USERS[$i]}" || FAILED=$((FAILED + 1))
done

echo ""
if [[ $FAILED -eq 0 ]]; then
    echo "✓ All ${#HOSTS[@]} host(s) deployed successfully."
else
    echo "✗ $FAILED of ${#HOSTS[@]} host(s) failed."
    exit 1
fi
