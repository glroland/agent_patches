#!/usr/bin/env bash
# deploy/linux/migrate_memory.sh — One-time migration of network, disk-trend,
# incident, and skill/responsibility-run state out of the flat attrs bucket
# into their own memory domains ("Network", "Disk Trends", "Incidents",
# "Skill States"), on every host in inventory.csv.
#
# Stops the agent_patches service before migrating: the migrate-memory tool
# and a live agent both read-modify-write attrs.json, and running them
# concurrently risks a lost update. The service is left stopped afterwards —
# restart it manually (e.g. after verifying the migration) once you're ready.
# Safe to re-run — migrate-memory is idempotent, and by default it leaves the
# old attrs.json keys in place as a backup (pass MIGRATE_PURGE=1 to remove
# them once you've verified the new domains look right on a host).
#
# Usage:
#   migrate_memory.sh <inventory.csv> <linux-binary>
#
# Optional environment variables:
#   SSH_USER         Login username on remote hosts (default: current user)
#   SSH_PORT         SSH port (default: 22)
#   SSH_KEY          Path to a private key (-i flag)
#   WINDOWS_USER     Login user on Windows hosts (default: Administrator)
#   WINDOWS_BINARY   Path to the compiled migrate-memory.exe (required if inventory has windows hosts)
#   MIGRATE_DRY_RUN  1 to preview changes without writing anything (default: 0)
#   MIGRATE_PURGE    1 to remove migrated keys from attrs.json after writing them (default: 0)
#   MEMORY_ROOT      Override the memory root path instead of reading it from the deployed config.yaml
#   MIGRATE_CONFIRM  1 (default) to prompt before each host, 0 to skip

set -uo pipefail

# ---------------------------------------------------------------------------
# Args
# ---------------------------------------------------------------------------
if [[ $# -lt 2 ]]; then
    echo "Usage: $0 <inventory.csv> <linux-binary>" >&2
    exit 1
fi

INVENTORY_CSV="$1"
BINARY="$2"

SSH_USER="${SSH_USER:-$USER}"
SSH_PORT="${SSH_PORT:-22}"
WINDOWS_USER="${WINDOWS_USER:-Administrator}"
WINDOWS_BINARY="${WINDOWS_BINARY:-}"
MIGRATE_DRY_RUN="${MIGRATE_DRY_RUN:-0}"
MIGRATE_PURGE="${MIGRATE_PURGE:-0}"
MEMORY_ROOT="${MEMORY_ROOT:-}"

for f in "$INVENTORY_CSV" "$BINARY"; do
    if [[ ! -f "$f" ]]; then
        echo "ERROR: required file not found: $f" >&2
        exit 1
    fi
done

# ---------------------------------------------------------------------------
# Parse inventory (same quote-aware CSV parser as deploy.sh, since the
# "tags" column may contain a comma inside quotes)
# ---------------------------------------------------------------------------
HOSTS=()
HOST_USERS=()
HOST_OSTYPES=()
while IFS=$'\x1f' read -r host os_type; do
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
done < <(awk -v OFS=$'\x1f' '
function parse_csv(line,    n, i, c, field, inquotes) {
    n = 0
    field = ""
    inquotes = 0
    for (i = 1; i <= length(line); i++) {
        c = substr(line, i, 1)
        if (c == "\"") { inquotes = !inquotes; continue }
        if (c == "," && !inquotes) { n++; f[n] = field; field = ""; continue }
        field = field c
    }
    n++
    f[n] = field
    return n
}
function trim(s) { gsub(/^[[:space:]]+|[[:space:]]+$/, "", s); return s }
NR>1 {
    delete f
    n = parse_csv($0)
    host = trim(f[2])
    ostype = trim(f[4])
    if (host != "") print host, ostype
}' "$INVENTORY_CSV")

if [[ ${#HOSTS[@]} -eq 0 ]]; then
    echo "ERROR: no hosts found in $INVENTORY_CSV" >&2
    exit 1
fi

echo "Migrating memory on ${#HOSTS[@]} host(s):"
for i in "${!HOSTS[@]}"; do
    echo "  ${HOST_USERS[$i]}@${HOSTS[$i]}  (${HOST_OSTYPES[$i]})"
done
echo ""
[[ "$MIGRATE_DRY_RUN" == "1" ]] && echo "DRY RUN — no changes will be written on any host."
if [[ "$MIGRATE_PURGE" == "1" ]]; then
    echo "PURGE enabled — migrated keys will be removed from attrs.json after writing."
else
    echo "PURGE disabled (default) — old attrs.json keys are left in place as a backup."
fi
echo ""

# Validate the Windows binary up front if any Windows hosts are in the inventory.
for os in "${HOST_OSTYPES[@]}"; do
    if [[ "$os" == "windows" ]]; then
        if [[ -z "$WINDOWS_BINARY" ]]; then
            echo "ERROR: inventory contains Windows host(s) but WINDOWS_BINARY is not set." >&2
            echo "       Set WINDOWS_BINARY=target/windows-x86_64/migrate-memory.exe" >&2
            exit 1
        fi
        if [[ ! -f "$WINDOWS_BINARY" ]]; then
            echo "ERROR: Windows binary not found: $WINDOWS_BINARY" >&2
            exit 1
        fi
        break
    fi
done

# ---------------------------------------------------------------------------
# SSH / sshpass setup (same pattern as deploy.sh / restart.sh)
# ---------------------------------------------------------------------------
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

SCP_OPTS=(
    -O
    -P "$SSH_PORT"
    -o StrictHostKeyChecking=accept-new
    -o ControlMaster=auto
    -o "ControlPath=$CTRL_DIR/%h-%p-%r"
    -o ControlPersist=120s
)
[[ -n "${SSH_KEY:-}" ]] && SCP_OPTS+=(-i "$SSH_KEY")

MIGRATE_PASS=""
if command -v sshpass &>/dev/null; then
    read -rsp "Migrate password (SSH + sudo, Enter for key-based auth): " MIGRATE_PASS
    echo
    export SSHPASS="$MIGRATE_PASS"
    SSH_CMD=(sshpass -e ssh)
    SCP_CMD=(sshpass -e scp)
else
    echo "Note: sshpass not found — using key-based SSH auth."
    read -rsp "Sudo password on remote hosts (Enter if passwordless sudo): " MIGRATE_PASS
    echo
    SSH_CMD=(ssh)
    SCP_CMD=(scp)
fi

# ---------------------------------------------------------------------------
# Remote scripts (written to local tempfiles, scp'd and executed — same
# pattern as deploy.sh's SETUP_SCRIPT). MIGRATE_DRY_RUN/MIGRATE_PURGE/
# MEMORY_ROOT are the same for every host in one run, so they're passed as
# positional args to the script rather than baked into its text.
# ---------------------------------------------------------------------------
SETUP_SCRIPT="/tmp/agent_patches_migrate_$$.sh"
WIN_SETUP_SCRIPT="/tmp/agent_patches_migrate_$$.ps1"
trap 'rm -f "$SETUP_SCRIPT" "$WIN_SETUP_SCRIPT"; rm -rf "$CTRL_DIR"' EXIT

cat > "$SETUP_SCRIPT" << 'REMOTE_SCRIPT'
#!/bin/bash
set -uo pipefail
INSTALL_ROOT=/opt/agent_patches
CONFIG_FILE="$INSTALL_ROOT/config/config.yaml"
DEFAULT_ROOT="$INSTALL_ROOT/data/memory"
MEMORY_ROOT_OVERRIDE="${1:-}"
DRY_RUN="${2:-0}"
PURGE="${3:-0}"

if [[ -n "$MEMORY_ROOT_OVERRIDE" ]]; then
    ROOT="$MEMORY_ROOT_OVERRIDE"
elif [[ -f "$CONFIG_FILE" ]]; then
    ROOT=$(awk '
        /^memory:/ { in_memory=1; next }
        in_memory && /^[a-zA-Z]/ { in_memory=0 }
        in_memory && /root:/ {
            sub(/^[^:]*:[[:space:]]*/, "")
            sub(/[[:space:]]*#.*/, "")
            gsub(/^[[:space:]"'"'"']+|[[:space:]"'"'"']+$/, "")
            print
            exit
        }
    ' "$CONFIG_FILE")
    [[ -n "$ROOT" ]] || ROOT="$DEFAULT_ROOT"
else
    ROOT="$DEFAULT_ROOT"
fi
echo "  → memory root: $ROOT"

chmod +x /tmp/migrate-memory

echo "  → stopping agent_patches..."
systemctl stop agent_patches 2>&1 || echo "    (service was not running)"

FLAGS=()
[[ "$DRY_RUN" == "1" ]] && FLAGS+=(-dry-run)
[[ "$PURGE" == "1" ]] && FLAGS+=(-purge)

echo "  → running migrate-memory ${FLAGS[*]:-}..."
rc=0
/tmp/migrate-memory -root "$ROOT" ${FLAGS[@]+"${FLAGS[@]}"} || rc=$?

echo "  → leaving agent_patches stopped (restart manually when ready)"

rm -f /tmp/migrate-memory
exit $rc
REMOTE_SCRIPT

cat > "$WIN_SETUP_SCRIPT" << 'WIN_REMOTE_SCRIPT'
param(
    [string]$MemoryRootOverride = "",
    [string]$DryRun = "0",
    [string]$Purge = "0"
)
$ErrorActionPreference = "Stop"
$InstallDir  = "C:\ProgramData\agent_patches"
$ConfigFile  = "$InstallDir\config\config.yaml"
$DefaultRoot = "$InstallDir\data\memory"
$ServiceName = "agent_patches"

if ($MemoryRootOverride -ne "") {
    $Root = $MemoryRootOverride
} elseif (Test-Path $ConfigFile) {
    $inMemory = $false
    $Root = ""
    foreach ($line in Get-Content $ConfigFile) {
        if ($line -match '^memory:') { $inMemory = $true; continue }
        if ($inMemory -and $line -match '^[a-zA-Z]') { $inMemory = $false }
        if ($inMemory -and $line -match 'root:\s*(.+)') {
            $Root = $Matches[1] -replace '\s*#.*$', '' -replace '^[\s"'']+|[\s"'']+$', ''
            break
        }
    }
    if ($Root -eq "") { $Root = $DefaultRoot }
} else {
    $Root = $DefaultRoot
}
Write-Output "  -> memory root: $Root"

Write-Output "  -> stopping $ServiceName..."
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne "Stopped") {
    Stop-Service -Name $ServiceName -Force
    Start-Sleep -Seconds 2
} else {
    Write-Output "     (service was not running)"
}

$flags = @()
if ($DryRun -eq "1") { $flags += "-dry-run" }
if ($Purge -eq "1") { $flags += "-purge" }

Write-Output "  -> running migrate-memory $flags..."
& "C:\Windows\Temp\migrate-memory.exe" -root "$Root" @flags
$rc = $LASTEXITCODE

Write-Output "  -> leaving $ServiceName stopped (restart manually when ready)"

Remove-Item -Path "C:\Windows\Temp\migrate-memory.exe" -Force -ErrorAction SilentlyContinue
exit $rc
WIN_REMOTE_SCRIPT

# ---------------------------------------------------------------------------
# Per-host migration (Linux)
# ---------------------------------------------------------------------------
migrate_host() {
    local host=$1
    local host_user=$2
    echo ""
    echo "┌─ $host"

    echo "│  Connecting as ${host_user}@${host}"
    if ! "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" true 2>&1 | sed 's/^/│  /'; then
        echo "│  ✗ FAILED: could not connect to $host"
        return 1
    fi

    echo "│  Copying migrate-memory binary and setup script..."
    if ! "${SCP_CMD[@]}" "${SCP_OPTS[@]}" "$BINARY" "$SETUP_SCRIPT" "${host_user}@${host}:/tmp/" 2>&1 | sed 's/^/│  /'; then
        echo "│  ✗ FAILED: could not copy files to $host"
        return 1
    fi

    local bin_base setup_base
    bin_base=$(basename "$BINARY")
    setup_base=$(basename "$SETUP_SCRIPT")
    "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
        "[ \"$bin_base\" = migrate-memory ] || mv /tmp/$bin_base /tmp/migrate-memory" 2>/dev/null || true

    echo "│  Running migration (stops agent_patches, leaves it stopped)..."
    local rc=0
    local remote_cmd="bash /tmp/$setup_base '$MEMORY_ROOT' '$MIGRATE_DRY_RUN' '$MIGRATE_PURGE'"
    if [[ "$host_user" == "root" ]]; then
        "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "$remote_cmd" 2>&1 | sed 's/^/│  /' || rc=$?
    elif [[ -n "$MIGRATE_PASS" ]]; then
        "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "sudo -S -p '' $remote_cmd" \
            2>&1 <<< "$MIGRATE_PASS" | sed 's/^/│  /' || rc=$?
    else
        "${SSH_CMD[@]}" -tt "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "sudo $remote_cmd" 2>&1 | sed 's/^/│  /' || rc=$?
    fi

    "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
        "rm -f /tmp/$setup_base" 2>/dev/null || true

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
# Per-host migration (Windows)
# ---------------------------------------------------------------------------
migrate_host_windows() {
    local host=$1
    local host_user=$2
    echo ""
    echo "┌─ $host (windows)"

    echo "│  Connecting as ${host_user}@${host}"
    if ! "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" "echo connected" 2>&1 | sed 's/^/│  /'; then
        echo "│  ✗ FAILED: could not connect to $host"
        return 1
    fi

    echo "│  Copying migrate-memory.exe and setup script..."
    if ! "${SCP_CMD[@]}" "${SCP_OPTS[@]}" "$WINDOWS_BINARY" "$WIN_SETUP_SCRIPT" \
            "${host_user}@${host}:C:/Windows/Temp/" 2>&1 | sed 's/^/│  /'; then
        echo "│  ✗ FAILED: could not copy files to $host"
        return 1
    fi

    local win_bin_base win_setup_base
    win_bin_base=$(basename "$WINDOWS_BINARY")
    win_setup_base=$(basename "$WIN_SETUP_SCRIPT")
    if [[ "$win_bin_base" != "migrate-memory.exe" ]]; then
        "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
            "powershell -NoProfile -Command \"Move-Item -Force C:\\Windows\\Temp\\$win_bin_base C:\\Windows\\Temp\\migrate-memory.exe\"" \
            2>/dev/null || true
    fi

    echo "│  Running migration (stops agent_patches, leaves it stopped)..."
    local rc=0
    "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
        "powershell -NoProfile -ExecutionPolicy Bypass \
            -File C:/Windows/Temp/${win_setup_base} \
            -DryRun ${MIGRATE_DRY_RUN} -Purge ${MIGRATE_PURGE} -MemoryRootOverride '${MEMORY_ROOT}'" \
        2>&1 | sed 's/^/│  /' || rc=$?

    "${SSH_CMD[@]}" "${SSH_BASE_OPTS[@]}" "${host_user}@${host}" \
        "powershell -NoProfile -Command \"Remove-Item -Force C:\\Windows\\Temp\\${win_setup_base} -ErrorAction SilentlyContinue\"" \
        2>/dev/null || true

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
# Main — iterate inventory
# ---------------------------------------------------------------------------
FAILED=0
for i in "${!HOSTS[@]}"; do
    if [[ "${MIGRATE_CONFIRM:-1}" == "1" ]]; then
        read -rp "Migrate memory on ${HOST_USERS[$i]}@${HOSTS[$i]} (${HOST_OSTYPES[$i]})? [Y/n] " _confirm
        if [[ "$_confirm" =~ ^[nN] ]]; then
            echo "  skipping ${HOSTS[$i]}"
            continue
        fi
    fi
    if [[ "${HOST_OSTYPES[$i]}" == "windows" ]]; then
        migrate_host_windows "${HOSTS[$i]}" "${HOST_USERS[$i]}" || FAILED=$((FAILED + 1))
    else
        migrate_host "${HOSTS[$i]}" "${HOST_USERS[$i]}" || FAILED=$((FAILED + 1))
    fi
done

echo ""
if [[ $FAILED -eq 0 ]]; then
    echo "✓ All ${#HOSTS[@]} host(s) migrated successfully."
else
    echo "✗ $FAILED of ${#HOSTS[@]} host(s) failed."
    exit 1
fi
