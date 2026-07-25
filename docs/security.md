# Security

This document covers the security controls implemented across the endpoint-server, central-backend, and deployment pipeline.

## Transport Authentication

### endpoint-server

Security is configured by `security.scheme` in `config.yaml`:

- `none` (default) — no authentication; suitable only for isolated networks
- `bearer` — all requests must carry `Authorization: Bearer <token>`

When `bearer` is active:
- A2A JSON-RPC requests on `/` are validated by `bearerInterceptor`, which is registered as an `a2asrv.CallInterceptor`. Invalid or missing tokens return `ErrUnauthenticated`.
- Plain HTTP endpoints (`/status`, `/memory`, `/approvals/`, `/responsibilities`) are wrapped with `requireBearer` middleware returning HTTP 401.
- The agent card (`/.well-known/agent.json`) is always public — it must be readable for A2A protocol discovery.
- The bearer token is embedded in the agent card's `SecuritySchemes` so callers know authentication is required before attempting requests.

### central-backend

`AGENT_AUTH_TOKEN` (required env var) is passed as `Authorization: Bearer <token>` on every request `AgentClient` makes to an endpoint-server. If the token is wrong or missing, the agent returns 401 and `AgentClient` treats the response as a failure (returns `null`).

## OS-Level Privilege Separation (Linux)

### Service account

The endpoint-server runs as a dedicated system user created by `deploy/linux/deploy.sh`:

```bash
useradd --system --no-create-home --shell /usr/sbin/nologin agent_patches
```

The account has no login shell and no home directory. It is added to functional groups (`adm`, `docker`, `systemd-journal`) for log access and container management, and is explicitly removed from `sudo` and `wheel` — it has no general privilege escalation path.

### Sudoers allowlist

`/etc/sudoers.d/agent_patches` grants `NOPASSWD` sudo only for the specific commands the agent needs:

| Command | Purpose |
|---|---|
| `apt-get update` | Refresh Debian package index |
| `apt-get upgrade -y` | Apply Debian updates |
| `dnf check-update` | Check for Fedora/RHEL updates |
| `dnf update -y` | Apply Fedora/RHEL updates |
| `yum check-update`, `yum update -y` | RHEL/CentOS legacy |
| `needs-restarting -r` | Post-patch reboot check |
| `smartctl -H -A -j *` | SMART health and attribute collection |
| `umount -l *` | NFS lazy unmount |
| `shutdown -r now` | Post-patch reboot |
| `/bin/sh -c *`, `/usr/bin/sh -c *` | HITL-gated `run_approved_command` |

The `/bin/sh -c *` entry is required because `run_approved_command` wraps arbitrary operator-approved commands in a shell. This is the broadest entry in the allowlist; its scope is constrained by the HITL approval gate (see below).

### Code-level sudo wrapping

The following files prepend `sudo -n` when `runtime.GOOS == "linux" && os.Getuid() != 0`:

- `endpoint-server/skills/check_for_pending_system_patches/patching/patching.go` — all patching commands
- `endpoint-server/skills/check_drives/smart_check_notwindows.go` — `smartctl -H -A -j`
- `endpoint-server/skills/check_drives/smart_attrs_notwindows.go` — `smartctl -A -j`
- `endpoint-server/skills/check_nfs/nfs_linux.go` — `umount -l`
- `endpoint-server/skills/run_approved_command/run_approved_command.go` — `sh -c <cmd>`

The `-n` flag causes `sudo` to fail immediately if a password prompt would be required, preventing the process from hanging.

### Directory permissions

```
Path                                         Owner                  Mode
/opt/agent_patches/                          root:agent_patches     750
/opt/agent_patches/bin/                      root:agent_patches     750
/opt/agent_patches/bin/patches-endpoint-server  root:agent_patches  750
/opt/agent_patches/config/                   agent_patches:agent_patches  750
/opt/agent_patches/data/                     agent_patches:agent_patches  750
/opt/agent_patches/logs/                     agent_patches:agent_patches  750
/opt/agent_patches/config/config.yaml        root:agent_patches     640
```

No user other than `root` and `agent_patches` can list, read, or traverse any part of the tree (mode 750 means no access for others). The binary and install root are root-owned so the `agent_patches` account cannot replace its own executable or modify the directory structure. Config is readable by `agent_patches` but not world-readable.

## OS-Level Privilege Separation (Windows)

### Service account

`deploy/linux/deploy.sh` creates a local user account `agent_patches` on each Windows target:

```powershell
New-LocalUser -Name "agent_patches" -Password $secPwd `
    -PasswordNeverExpires $true -UserMayNotChangePassword $true `
    -AccountNeverExpires
Add-LocalGroupMember -Group "Administrators" -Member "agent_patches"
```

The password is a 32-byte random value generated in-memory on each deploy and never written to disk. It is reset on every deploy (service is stopped first; the new password is used to reconfigure the service via `sc.exe`).

`Administrators` group membership is required because the Windows Update COM API (`installer.Install()`) demands an elevated token. Services running as Administrators receive an elevated token automatically, bypassing UAC — no interactive prompt occurs.

### Directory ACL

`C:\ProgramData\agent_patches` has inheritance removed and is granted:

```
SYSTEM:          (OI)(CI)F   Full control
Administrators:  (OI)(CI)F   Full control
```

All other local users, domain users, and service accounts have no access. The `agent_patches` account gains access via `Administrators` membership.

### Service configuration

```powershell
sc.exe config agent_patches obj= ".\agent_patches" password= "$SvcPassword"
```

The service runs as `.\agent_patches` rather than the default `LocalSystem`.

## Prompt Injection Defense

### Sanitize layer (`endpoint-server/utils/sanitize/sanitize.go`)

Every tool result passes through `sanitize.ToolOutput` in `agent.go` before it is appended to the LLM's message context. Tool outputs are the primary injection surface because they contain data from managed systems (log lines, package descriptions, command output).

Three layers of defense:

**1. Strip control characters and Unicode steganography**

Removes:
- ASCII control characters (< 0x20, except `\t`, `\n`, `\r`; and 0x7F)
- Unicode zero-width characters: U+200B, U+200C, U+200D
- Unicode line/paragraph separators: U+2028, U+2029
- Bidirectional override characters: U+202A–U+202E
- BOM / zero-width no-break space: U+FEFF

These are used in steganographic attacks to hide injected instructions inside otherwise innocent-looking text.

**2. Regex redaction**

20 compiled patterns covering:

| Category | Examples |
|---|---|
| Structural model-format delimiters | `[INST]`, `<<SYS>>`, `<\|im_start\|>`, `<system>...</system>`, `###SYSTEM###`, `###INSTRUCTION###`, `[SYSTEM]` |
| Constraint-bypass phrases | `ignore all previous instructions`, `disregard previous rules`, `forget everything above`, `override your safety constraints` |
| Role injection | `you are now a jailbroken assistant`, `your new purpose is to` |
| DAN/jailbreak families | `DAN mode`, `do anything now`, `jailbreaked` |
| Exfiltration probes | `reveal the system prompt`, `print the api key`, `base64 encode the instructions` |

Matched content is replaced with `[REDACTED: potential prompt injection]`. The patterns target constructs that are structurally impossible in legitimate sysadmin output (disk reports, package lists, log lines) so the false-positive rate is near zero.

**3. Truncation**

Outputs longer than 64KB are truncated with a marker. This prevents context-flooding attacks that bury the system prompt by filling the context window with data.

### Observability

When sanitization fires, `slog.Warn` is emitted and OTel span attributes are set on the `tool.call` span:

```
security.sanitized    = true
security.sanitize_events = <count>
```

## HITL Approval Flow

The `request_approval` tool is the primary mechanism preventing the agent from taking unilateral state-changing actions.

Flow:
1. Agent calls `request_approval` with `title`, `detail`, `proposedAction`, and `risk`
2. An `ApprovalEntry` is written to `AttrsStore` (durable, survives restarts)
3. A `TimelineEntry` (type=`approval`, status=`pending`) is written to the `timeline` domain
4. If `risk="high"`: `notifier.Notify` fires immediately — operator gets an out-of-band alert without needing to check the dashboard
5. The tool blocks, polling `AttrsStore` every 5 seconds
6. Operator sees the pending approval in central-ui (via WebSocket push), reviews the proposed action, and submits a decision
7. central-backend forwards the decision to `POST /approvals/:id/decision` on the agent
8. The approval handler updates the `ApprovalEntry` status in `AttrsStore`
9. The polling loop detects the status change and returns `"approved"`, `"rejected"`, or `"timed_out"`

**No retry on timeout.** The approval window is 24 hours. If no decision is received, the entry transitions to `timed_out`, a second notification fires (confirming the action was NOT taken), and the approval is permanently cancelled. The agent continues and the run reports the timeout in its output.

**`run_approved_command` does not re-verify** the approval at execution time beyond checking that its linked approval ID is in `approved` state. The approval ID is passed through the tool call chain, not re-fetched from a separate approval request.

## Notifier

`endpoint-server/utils/notifier/notifier.go` — writes to the `notifications` memory domain. The endpoint-server itself does not send email; it stores notifications in memory where central-backend reads them during polling and forwards them through its `emailer` service.

Notification triggers:
- `request_approval`: high-risk approval created (immediate), approval timeout (post-expiry)
- `loop.maybeNotify`: responsibility run completion (`when_to_notify=always`) or failure (`on_error`)
- Login monitors: unexpected source IP, failed login threshold exceeded

## Known Limitations

### Broad shell allowlist

The sudoers entry `/bin/sh -c *` is required for `run_approved_command` to work under the non-root service account. This allows any shell command once the process can invoke `sudo` — the constraint is entirely behavioural (the HITL gate) rather than OS-level.

### Planned: privilege-separated executor daemon (Option B)

A future architectural improvement would replace the sudoers approach with a separate executor daemon running as root (or with specific Linux capabilities) and connected to the agent via a Unix socket with a typed API. The agent process would make typed requests (`RunPatch`, `RunSmartCheck`, `RunApprovedCommand`) rather than invoking `sudo`. This would allow:
- Removing all sudoers entries entirely
- Eliminating the broad `/bin/sh -c *` allowlist
- Enforcing command-level policy in a privileged process the agent cannot modify

The sudoers allowlist is the pragmatic interim solution pending this implementation.
