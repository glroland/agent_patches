#!/usr/bin/env bash
# deploy/linux/deploy.sh — Deploy patches-endpoint-server to every host in inventory.csv.
#
# Usage:
#   deploy.sh <inventory.csv> <binary> <service-file> <config-file>
#
# Optional environment variables:
#   SSH_USER                Login username on remote hosts (default: current user)
#   SSH_PORT                SSH port (default: 22)
#   SSH_KEY                 Path to a private key (-i flag)
#   LINUX_RESPONSIBILITIES  Path to linux-responsibilities.yaml (optional)
#   WINDOWS_RESPONSIBILITIES Path to windows-responsibilities.yaml (optional)

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

# Windows-specific env vars (only required when inventory contains windows hosts).
#   WINDOWS_BINARY           Path to the compiled patches-endpoint-server.exe
#   WINDOWS_CONFIG           Path to the Windows config file
#   WINDOWS_USER             Login user on Windows hosts (default: Administrator)
#   WINDOWS_RESPONSIBILITIES Path to windows-responsibilities.yaml
WINDOWS_BINARY="${WINDOWS_BINARY:-}"
WINDOWS_CONFIG="${WINDOWS_CONFIG:-}"
WINDOWS_USER="${WINDOWS_USER:-Administrator}"
WINDOWS_RESPONSIBILITIES="${WINDOWS_RESPONSIBILITIES:-}"

# Linux-specific env vars.
#   LINUX_RESPONSIBILITIES  Path to linux-responsibilities.yaml
LINUX_RESPONSIBILITIES="${LINUX_RESPONSIBILITIES:-}"

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

echo "Deploying patches-endpoint-server to ${#HOSTS[@]} host(s):"
for i in "${!HOSTS[@]}"; do
    echo "  ${HOST_USERS[$i]}@${HOSTS[$i]}  (${HOST_OSTYPES[$i]})"
done
echo ""

# Validate Windows-specific files if any Windows hosts are in the inventory.
for os in "${HOST_OSTYPES[@]}"; do
    if [[ "$os" == "windows" ]]; then
        if [[ -z "$WINDOWS_BINARY" ]]; then
            echo "ERROR: inventory contains Windows host(s) but WINDOWS_BINARY is not set." >&2
            echo "       Set WINDOWS_BINARY=target/windows-x86_64/patches-endpoint-server.exe" >&2
            exit 1
        fi
        if [[ ! -f "$WINDOWS_BINARY" ]]; then
            echo "ERROR: Windows binary not found: $WINDOWS_BINARY" >&2
            exit 1
        fi
        if [[ -n "$WINDOWS_CONFIG" && ! -f "$WINDOWS_CONFIG" ]]; then
            echo "ERROR: Windows config not found: $WINDOWS_CONFIG" >&2
            exit 1
        fi
        break
    fi
done

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
WIN_SETUP_SCRIPT=$(mktemp /tmp/agent_patches_setup.XXXXXX.ps1)
trap 'rm -f "$SETUP_SCRIPT" "$WIN_SETUP_SCRIPT"; rm -rf "$CTRL_DIR"' EXIT

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

# ── OS responsibilities ───────────────────────────────────────────────────────
step "Deploying OS responsibilities..."
if [[ -f /tmp/linux-responsibilities.yaml ]]; then
    install -o "$SERVICE_USER" -g "$SERVICE_USER" -m 640 \
        /tmp/linux-responsibilities.yaml "$INSTALL_ROOT/config/linux-responsibilities.yaml"
    ok "responsibilities written to $INSTALL_ROOT/config/linux-responsibilities.yaml"
else
    warn "no linux-responsibilities.yaml found in /tmp — skipping (existing file preserved)"
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
      /tmp/linux-responsibilities.yaml \
      /tmp/agent_patches_setup.*.sh
ok "done"
REMOTE_SCRIPT

cat > "$WIN_SETUP_SCRIPT" << 'WIN_REMOTE_SCRIPT'
# Windows setup script - runs on the remote host via PowerShell over SSH.
# Files are SCP'd to C:\Windows\Temp\ before this script runs.
param(
    [string]$ConfigFile = "",
    [string]$ResponsibilitiesFile = ""
)

$ErrorActionPreference = "Stop"
$Tmp         = "C:\Windows\Temp"
$ServiceName = "agent_patches"
$InstallDir  = "C:\ProgramData\agent_patches"
$BinDir      = "$InstallDir\bin"
$ConfigDir   = "$InstallDir\config"
$BinaryDst   = "$BinDir\patches-endpoint-server.exe"
$ConfigDst   = "$ConfigDir\config.yaml"

function Step { param([string]$m) Write-Output "  -> $m" }
function OK   { param([string]$m) Write-Output "     OK $m" }
function Warn { param([string]$m) Write-Output "  WARNING: $m" }

Step "Creating directories under $InstallDir..."
foreach ($d in $BinDir, $ConfigDir, "$InstallDir\data", "$InstallDir\logs") {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
}
OK "directories ready"

Step "Stopping existing service/process..."
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne "Stopped") {
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
    OK "service stopped"
} else {
    Get-Process "patches-endpoint-server" -ErrorAction SilentlyContinue |
        Stop-Process -Force -ErrorAction SilentlyContinue
    OK "no running service"
}

Step "Installing binary..."
Copy-Item -Path "$Tmp\patches-endpoint-server.exe" -Destination $BinaryDst -Force
OK "installed to $BinaryDst"

Step "Installing config..."
if ($ConfigFile -ne "" -and (Test-Path "$Tmp\$ConfigFile")) {
    Copy-Item -Path "$Tmp\$ConfigFile" -Destination $ConfigDst -Force
    OK "config written to $ConfigDst"
} else {
    Warn "no config supplied - existing config preserved"
}

Step "Installing OS responsibilities..."
if ($ResponsibilitiesFile -ne "" -and (Test-Path "$Tmp\$ResponsibilitiesFile")) {
    Copy-Item -Path "$Tmp\$ResponsibilitiesFile" -Destination "$ConfigDir\windows-responsibilities.yaml" -Force
    OK "responsibilities written to $ConfigDir\windows-responsibilities.yaml"
} else {
    Warn "no responsibilities file supplied - existing file preserved"
}

Step "Configuring Windows service..."
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc) {
    & sc.exe config $ServiceName binPath= "`"$BinaryDst`"" start= auto | Out-Null
    OK "service updated"
} else {
    & sc.exe create $ServiceName binPath= "`"$BinaryDst`"" start= auto DisplayName= "agent_patches" | Out-Null
    OK "service created"
}

Step "Setting AGENT_PATCHES_CONFIG for service..."
$regKey = "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName"
Set-ItemProperty -Path $regKey -Name "Environment" `
    -Value @("AGENT_PATCHES_CONFIG=$ConfigDst") -Type MultiString
OK "env var set"

Step "Starting service..."
Start-Service -Name $ServiceName
$status = (Get-Service -Name $ServiceName).Status
OK "service is $status"

Step "Cleaning up..."
Get-ChildItem -Path $Tmp -Filter "agent_patches_setup*.ps1" |
    Remove-Item -Force -ErrorAction SilentlyContinue
Remove-Item -Path "$Tmp\patches-endpoint-server.exe" -Force -ErrorAction SilentlyContinue
if ($ConfigFile -ne "") {
    Remove-Item -Path "$Tmp\$ConfigFile" -Force -ErrorAction SilentlyContinue
}
if ($ResponsibilitiesFile -ne "") {
    Remove-Item -Path "$Tmp\$ResponsibilitiesFile" -Force -ErrorAction SilentlyContinue
}
OK "done"
WIN_REMOTE_SCRIPT

# ---------------------------------------------------------------------------
# Per-host deploy (Windows)
# ---------------------------------------------------------------------------
deploy_host_windows() {
    local host=$1
    local host_user=$2
    local win_tmp="C:/Windows/Temp"
    echo ""
    echo "┌─ $host (windows)"

    # Prime the ControlMaster — one auth for all subsequent steps.
    echo "│  Connecting as ${host_user}@${host}"
    if ! "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "echo connected" 2>&1 | sed 's/^/│  /'; then
        echo "│  ✗ FAILED: could not connect to $host"
        return 1
    fi

    # Build file list. Files go to C:\Windows\Temp\ on the remote host.
    local cfg_base=""
    local resp_base=""
    local files=("$WINDOWS_BINARY" "$WIN_SETUP_SCRIPT")
    local win_cfg="${WINDOWS_CONFIG:-$CONFIG_FILE}"
    if [[ -f "$win_cfg" ]]; then
        cfg_base=$(basename "$win_cfg")
        files+=("$win_cfg")
    else
        echo "│  ⚠ WARNING: config file not found ($win_cfg) — skipping config deploy"
    fi
    if [[ -n "$WINDOWS_RESPONSIBILITIES" && -f "$WINDOWS_RESPONSIBILITIES" ]]; then
        resp_base=$(basename "$WINDOWS_RESPONSIBILITIES")
        files+=("$WINDOWS_RESPONSIBILITIES")
    elif [[ -n "$WINDOWS_RESPONSIBILITIES" ]]; then
        echo "│  ⚠ WARNING: windows-responsibilities.yaml not found ($WINDOWS_RESPONSIBILITIES) — skipping"
    fi

    local win_setup_base
    win_setup_base=$(basename "$WIN_SETUP_SCRIPT")

    echo "│  Copying files..."
    if ! "${SCP_CMD[@]}" "${SCP_OPTS[@]}" "${files[@]}" \
            "${host_user}@${host}:${win_tmp}/" 2>&1 | sed 's/^/│  /'; then
        echo "│  ✗ FAILED: could not copy files to $host"
        return 1
    fi

    echo "│  Running setup..."
    local rc=0
    "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
        "powershell -NoProfile -ExecutionPolicy Bypass \
            -File ${win_tmp}/${win_setup_base} \
            -ConfigFile ${cfg_base} \
            -ResponsibilitiesFile ${resp_base}" \
        2>&1 | sed 's/^/│  /' || rc=$?

    if [[ $rc -eq 0 ]]; then
        echo "└─ ✓ $host — done"
    else
        echo "└─ ✗ $host — FAILED (exit $rc)"
    fi

    "${SSH_CMD[@]}" -O exit -o "ControlPath=$CTRL_DIR/%h-%p-%r" \
        "${host_user}@${host}" 2>/dev/null || true

    return $rc
}

# ---------------------------------------------------------------------------
# Per-host deploy (Linux)
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
    if [[ -n "$LINUX_RESPONSIBILITIES" && -f "$LINUX_RESPONSIBILITIES" ]]; then
        files+=("$LINUX_RESPONSIBILITIES")
    elif [[ -n "$LINUX_RESPONSIBILITIES" ]]; then
        echo "│  ⚠ WARNING: linux-responsibilities.yaml not found ($LINUX_RESPONSIBILITIES) — skipping"
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

    # Rename the responsibilities file to the expected name on the remote
    if [[ -n "$LINUX_RESPONSIBILITIES" && -f "$LINUX_RESPONSIBILITIES" ]]; then
        local resp_base
        resp_base=$(basename "$LINUX_RESPONSIBILITIES")
        if [[ "$resp_base" != "linux-responsibilities.yaml" ]]; then
            "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
                "mv /tmp/$resp_base /tmp/linux-responsibilities.yaml" 2>/dev/null || true
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
    if [[ "${HOST_OSTYPES[$i]}" == "windows" ]]; then
        deploy_host_windows "${HOSTS[$i]}" "${HOST_USERS[$i]}" || FAILED=$((FAILED + 1))
    else
        deploy_host "${HOSTS[$i]}" "${HOST_USERS[$i]}" || FAILED=$((FAILED + 1))
    fi
done

echo ""
if [[ $FAILED -eq 0 ]]; then
    echo "✓ All ${#HOSTS[@]} host(s) deployed successfully."
else
    echo "✗ $FAILED of ${#HOSTS[@]} host(s) failed."
    exit 1
fi
