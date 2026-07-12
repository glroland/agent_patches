#!/usr/bin/env bash
# deploy/linux/stop_agents.sh — Stop the agent_patches service on every host in inventory.csv.
# Does not touch the binary, config, or any other deployed files — just stops the daemon.
#
# Usage:
#   stop_agents.sh <inventory.csv>
#
# Optional environment variables:
#   SSH_USER      Login username on remote hosts (default: current user)
#   SSH_PORT      SSH port (default: 22)
#   SSH_KEY       Path to a private key (-i flag)
#   WINDOWS_USER  Login user on Windows hosts (default: Administrator)

set -uo pipefail

# ---------------------------------------------------------------------------
# Args
# ---------------------------------------------------------------------------
if [[ $# -lt 1 ]]; then
    echo "Usage: $0 <inventory.csv>" >&2
    exit 1
fi

INVENTORY_CSV="$1"

SSH_USER="${SSH_USER:-$USER}"
SSH_PORT="${SSH_PORT:-22}"
WINDOWS_USER="${WINDOWS_USER:-Administrator}"

if [[ ! -f "$INVENTORY_CSV" ]]; then
    echo "ERROR: inventory file not found: $INVENTORY_CSV" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Parse inventory
# ---------------------------------------------------------------------------
HOSTS=()
HOST_USERS=()
HOST_OSTYPES=()
while IFS='|' read -r host os_type; do
    [[ -n "$host" ]] || continue
    case "$os_type" in
        rhel)    user="root" ;;
        ubuntu)  user="lroland" ;;
        windows) user="$WINDOWS_USER" ;;
        *)       user="${SSH_USER}" ;;
    esac
    HOSTS+=("$host")
    HOST_USERS+=("$user")
    HOST_OSTYPES+=("$os_type")
done < <(awk -F',' 'NR>1 {
    gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", $2)
    gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", $4)
    if ($2 != "") print $2 "|" $4
}' "$INVENTORY_CSV")

if [[ ${#HOSTS[@]} -eq 0 ]]; then
    echo "ERROR: no hosts found in $INVENTORY_CSV" >&2
    exit 1
fi

echo "Stopping agent_patches on ${#HOSTS[@]} host(s):"
for i in "${!HOSTS[@]}"; do
    echo "  ${HOST_USERS[$i]}@${HOSTS[$i]}  (${HOST_OSTYPES[$i]})"
done
echo ""

# ---------------------------------------------------------------------------
# SSH / sshpass setup
# ---------------------------------------------------------------------------
CTRL_DIR=$(mktemp -d /tmp/agent_patches_ssh.XXXXXX)
trap 'rm -rf "$CTRL_DIR"' EXIT

SSH_BASE_OPTS=(
    -o StrictHostKeyChecking=accept-new
    -o ConnectTimeout=10
    -o BatchMode=no
    -o ControlMaster=auto
    -o "ControlPath=$CTRL_DIR/%h-%p-%r"
    -o ControlPersist=60s
    -p "$SSH_PORT"
)
[[ -n "${SSH_KEY:-}" ]] && SSH_BASE_OPTS+=(-i "$SSH_KEY")

STOP_PASS=""

if command -v sshpass &>/dev/null; then
    read -rsp "Stop password (SSH + sudo, Enter for key-based auth): " STOP_PASS
    echo
    export SSHPASS="$STOP_PASS"
    SSH_CMD=(sshpass -e ssh)
else
    echo "Note: sshpass not found — using key-based SSH auth."
    read -rsp "Sudo password on remote hosts (Enter if passwordless sudo): " STOP_PASS
    echo
    SSH_CMD=(ssh)
fi

# ---------------------------------------------------------------------------
# Per-host stop (Linux)
# ---------------------------------------------------------------------------
stop_host() {
    local host=$1
    local host_user=$2
    echo ""
    echo "┌─ $host"

    local rc=0
    if [[ "$host_user" == "root" ]]; then
        # RHEL: logging in as root, no sudo needed
        "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "systemctl stop agent_patches && systemctl is-active agent_patches" \
            2>&1 | sed 's/^/│  /' || rc=$?
    elif [[ -n "$STOP_PASS" ]]; then
        # Ubuntu: non-root login, feed password to sudo -S
        "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "sudo -S -p '' bash -c 'systemctl stop agent_patches && systemctl is-active agent_patches'" \
            2>&1 <<< "$STOP_PASS" | sed 's/^/│  /' || rc=$?
    else
        # Passwordless sudo
        "${SSH_CMD[@]}" -tt "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "sudo systemctl stop agent_patches && sudo systemctl is-active agent_patches" \
            2>&1 | sed 's/^/│  /' || rc=$?
    fi

    # "systemctl is-active" exits non-zero for a stopped service — that is the
    # success case here, so only treat this as a failure if the stop itself
    # (not the status check) errored out.
    if [[ $rc -eq 0 || $rc -eq 3 ]]; then
        echo "└─ ✓ $host — stopped"
        rc=0
    else
        echo "└─ ✗ $host — FAILED (exit $rc)"
    fi

    "${SSH_CMD[@]}" -O exit -o "ControlPath=$CTRL_DIR/%h-%p-%r" \
        "${host_user}@${host}" 2>/dev/null || true

    return $rc
}

# ---------------------------------------------------------------------------
# Per-host stop (Windows)
# ---------------------------------------------------------------------------
stop_host_windows() {
    local host=$1
    local host_user=$2
    echo ""
    echo "┌─ $host (windows)"

    local rc=0
    "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
        "powershell -NoProfile -Command \"Stop-Service -Name agent_patches -Force; (Get-Service -Name agent_patches).Status\"" \
        2>&1 | sed 's/^/│  /' || rc=$?

    if [[ $rc -eq 0 ]]; then
        echo "└─ ✓ $host — stopped"
    else
        echo "└─ ✗ $host — FAILED (exit $rc)"
    fi

    "${SSH_CMD[@]}" -O exit -o "ControlPath=$CTRL_DIR/%h-%p-%r" \
        "${host_user}@${host}" 2>/dev/null || true

    return $rc
}

# ---------------------------------------------------------------------------
# Main — iterate inventory
# ---------------------------------------------------------------------------
FAILED=0
for i in "${!HOSTS[@]}"; do
    if [[ "${HOST_OSTYPES[$i]}" == "windows" ]]; then
        stop_host_windows "${HOSTS[$i]}" "${HOST_USERS[$i]}" || FAILED=$((FAILED + 1))
    else
        stop_host "${HOSTS[$i]}" "${HOST_USERS[$i]}" || FAILED=$((FAILED + 1))
    fi
done

echo ""
if [[ $FAILED -eq 0 ]]; then
    echo "✓ All ${#HOSTS[@]} host(s) stopped successfully."
else
    echo "✗ $FAILED of ${#HOSTS[@]} host(s) failed."
    exit 1
fi
