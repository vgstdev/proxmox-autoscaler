# Proxmox Autoscaler

[![Release](https://img.shields.io/github/v/release/vgstdev/proxmox-autoscaler)](https://github.com/vgstdev/proxmox-autoscaler/releases)
[![Build](https://github.com/vgstdev/proxmox-autoscaler/actions/workflows/release.yml/badge.svg)](https://github.com/vgstdev/proxmox-autoscaler/actions)
[![License](https://img.shields.io/github/license/vgstdev/proxmox-autoscaler)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/vgstdev/proxmox-autoscaler)](go.mod)

We believe this is the **first open-source autoscaling engine for Proxmox**. The Kubernetes Horizontal Pod Autoscaler equivalent for Proxmox — a lightweight daemon written in Go that monitors LXC containers and automatically adjusts their CPU and RAM in response to sustained resource saturation, then reverts the allocation only after usage has stayed back within a safer range.

Because it operates entirely through the Proxmox REST API, it can run directly on the Proxmox host itself or on any external machine that has network access to the API — no agent installation inside the containers is required.

## Features

- **Independent per-resource scaling** — CPU and RAM are monitored and scaled independently
- **Temporary boosts with downscale hysteresis** — resources are increased for a configurable duration (default 2 minutes) and reverted only after sustained usage below the downscale threshold
- **Graceful fallback** — tries +50% first; falls back to +25% if host capacity is insufficient; skips if neither fits
- **Persistent state** — boost state survives service restarts via SQLite; on startup the service reconciles live Proxmox config against stored state
- **Manual change detection** — if an administrator changes a container's resources from the Proxmox UI while a boost is active, the service detects the discrepancy and adopts the new value as the baseline without overwriting it
- **Email and Slack notifications** — sends alerts on boost and revert via email (system mail utility) and/or Slack (Bot API), in English or Spanish
- **Structured logging** — logs to stdout (journald) and optionally to a file; only meaningful events are logged (no per-poll noise)
- **LXC only** — QEMU/KVM virtual machines are intentionally ignored
- **Exclusion tag** — containers tagged with a configurable tag (default: `noautoscale`) are skipped

## Requirements

- Proxmox VE 7.x, 8.x or 9.x
- A Proxmox API token with sufficient permissions (see below)
- Network access to the Proxmox API (`https://<host>:8006`) from wherever the service runs
- A working system mail utility (e.g. `mailutils`, `msmtp`) if email notifications are enabled
- A Slack Bot token and channel ID if Slack notifications are enabled

## How it works

```
Every 5 seconds (configurable):

  For each running LXC container (excluding tagged ones):

    CPU saturation  = status.cpu              ≥ 95%
    RAM saturation  = mem_used / mem_total    ≥ 95%

    If saturated for 3 consecutive polls (15 s):
      → Try to apply +50% boost (capped by available host capacity)
      → Fall back to +25% if +50% doesn't fit
      → Skip with a warning if neither fits

    After boost_duration (2 min):
      → Re-read container config from Proxmox API
      → If value was changed manually: adopt it as new baseline, cancel boost
      → Otherwise: revert only if usage stayed below 80% for 6 consecutive polls
      → If usage is still too high: keep the boost and check again on the next cycle
      → Log and email only when the revert actually happens
```

## Installation

### Automated deployment script (recommended)

The `scripts/deploy.sh` script handles the full installation, update, and uninstall lifecycle:

```bash
# Download and run directly on the target machine
curl -fsSL https://raw.githubusercontent.com/vgstdev/proxmox-autoscaler/main/scripts/deploy.sh | sudo bash

# Or clone first and run locally
sudo ./scripts/deploy.sh             # install latest release
sudo ./scripts/deploy.sh v1.2.0      # install specific version
sudo ./scripts/deploy.sh --force     # reinstall current version
sudo ./scripts/deploy.sh --uninstall # remove everything
```

The script will:
- Detect the host architecture (amd64 / arm64) automatically
- Download the correct binary from the GitHub release
- Create all required directories
- Install the systemd service and enable it
- **Never overwrite an existing config** — saves the new example as `autoscaler.yaml.new` instead
- On update: stop the service first (triggering a graceful revert of active boosts), replace the binary, and restart

### Manual installation

The service communicates with Proxmox exclusively through its REST API, so it can be installed in two ways:

- **On the Proxmox host itself** — simplest setup, no firewall rules needed.
- **On an external machine** — any Linux host that can reach `https://<proxmox-ip>:8006`. Useful if you prefer to keep monitoring tooling separate from the hypervisor.

Both deployments are identical; only the value of `proxmox.host` in the config changes.

### From a release binary

1. Download the latest release for your architecture from the [Releases](https://github.com/vgstdev/proxmox-autoscaler/releases) page.

   ```bash
   # Example for amd64
   tar -xzf proxmox-autoscaler_<version>_linux_amd64.tar.gz
   ```

2. Copy the binary, config, and service unit to the target machine (Proxmox host or external):

   ```bash
   scp proxmox-autoscaler root@<target-ip>:/usr/local/bin/
   scp autoscaler.yaml    root@<target-ip>:/tmp/
   scp proxmox-autoscaler.service root@<target-ip>:/etc/systemd/system/
   ```

3. On the target machine:

   ```bash
   chmod +x /usr/local/bin/proxmox-autoscaler
   mkdir -p /etc/proxmox-autoscaler
   mv /tmp/autoscaler.yaml /etc/proxmox-autoscaler/autoscaler.yaml
   # Edit the config before starting (see Configuration section)
   nano /etc/proxmox-autoscaler/autoscaler.yaml
   systemctl daemon-reload
   systemctl enable proxmox-autoscaler
   systemctl start proxmox-autoscaler
   ```

### From source

Requires Go 1.21+.

```bash
git clone https://github.com/vgstdev/proxmox-autoscaler.git
cd proxmox-autoscaler

# Build for Linux amd64 (from any OS)
make build-linux
# Output: bin/proxmox-autoscaler-linux-amd64
```

## Creating a Proxmox API Token

1. In the Proxmox web UI go to **Datacenter → Permissions → API Tokens → Add**
2. Select a user (e.g. `root@pam`), set Token ID to `autoscaler`
3. **Uncheck** "Privilege Separation" so the token inherits the user's permissions
4. Copy the displayed secret — it is only shown once

The resulting `token_id` value will be `root@pam!autoscaler`.

## Configuration

The default config path is `/etc/proxmox-autoscaler/autoscaler.yaml`. Override it with the `-config` flag.

```yaml
proxmox:
  host: "https://192.168.1.10:8006"   # Proxmox host URL
  node: "pve"                          # Node name as shown in the UI
  token_id: "root@pam!autoscaler"      # user@realm!token-name
  token_secret: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
  insecure_tls: false                  # Set true for self-signed certificates

monitor:
  poll_interval: 5s                   # How often to check each container
  saturation_threshold: 0.95          # Fraction of allocated resource considered saturated (0–1)
  downscale_threshold: 0.80           # Boost is kept until usage stays strictly below this threshold
  consecutive_samples: 3              # Consecutive saturated polls required before boosting
  downscale_consecutive_samples: 6    # Consecutive safe polls required before reverting a boost
  boost_duration: 2m                  # Minimum time a boost stays active before downscale is considered
  history_samples: 10                 # Rolling window size for pre-boost average calculation

scaling:
  # Which CPU field to scale:
  #   "cores"    — number of visible cores (integer). Use when cpulimit=0 (unlimited).
  #   "cpulimit" — CPU throttle in core-fractions (float, e.g. 2.5). Use when cpulimit > 0.
  cpu_resource: "cores"
  primary_boost_factor: 1.5    # First attempt: +50%
  fallback_boost_factor: 1.25  # Second attempt if host has insufficient capacity: +25%
  exclude_tag: "noautoscale"   # LXC containers with this tag are never scaled
  host_cpu_max_threshold: 0.9    # Skip CPU boost if host CPU usage >= 90%
  host_memory_max_threshold: 0.9 # Skip memory boost if host memory usage >= 90%

notifications:
  email:
    enabled: true
    mail_binary: "/usr/bin/mail"  # Path to system mail binary
    to: "admin@example.com"       # Notification recipient
    language: "es"                # Email language: "es" (Spanish) | "en" (English)
  slack:
    enabled: false
    token: "xoxb-xxxxxxxxxxxx-xxxxxxxxxxxx-xxxxxxxxxxxxxxxxxxxxxxxx"  # Slack Bot token
    channel: "C0XXXXXXXXX"        # Slack channel ID

logging:
  level: "info"    # debug | info | warn | error
  format: "text"   # text  | json
  # Optional log file. Leave empty to log only to stdout (journald).
  file: "/var/log/proxmox-autoscaler/autoscaler.log"

storage:
  # SQLite database for boost state persistence.
  # The directory is created automatically if it does not exist.
  db_path: "/var/lib/proxmox-autoscaler/state.db"
```

### Setting up Slack notifications

1. Go to [https://api.slack.com/apps](https://api.slack.com/apps) and click **Create New App → From scratch**.
2. Give it a name (e.g. `Proxmox Autoscaler`) and select your workspace.
3. In the left menu go to **OAuth & Permissions**.
4. Under **Bot Token Scopes** add the scope `chat:write`.
5. Click **Install to Workspace** and authorise the app.
6. Copy the **Bot User OAuth Token** (starts with `xoxb-`). This is your `token`.
7. Invite the bot to the target channel: in Slack open the channel, type `/invite @Proxmox Autoscaler` (or whatever you named it).
8. Get the channel ID: right-click the channel name → **View channel details** → copy the ID at the bottom (e.g. `C0XXXXXXXXX`). This is your `channel`.
9. Set both values in the config and set `enabled: true`:

```yaml
notifications:
  slack:
    enabled: true
    token: "xoxb-xxxxxxxxxxxx-xxxxxxxxxxxx-xxxxxxxxxxxxxxxxxxxxxxxx"
    channel: "C0XXXXXXXXX"
```

### Excluding a container

Add the `noautoscale` tag to any LXC container from the Proxmox UI (**Container → Options → Tags**) or via CLI:

```bash
pct set <vmid> --tags noautoscale
```

The tag name is configurable via `scaling.exclude_tag`.

## Log events reference

The service only logs meaningful events. Per-poll status checks are intentionally not logged.

| Level | Event |
|-------|-------|
| `INFO` | Service started / stopped |
| `INFO` | DB opened (new or existing) |
| `INFO` | Boost applied (vmid, resource, original → new value, factor) |
| `INFO` | Boost reverted — normal (usage returned to pre-boost levels) |
| `INFO` | Boost retained — usage not stable enough to downscale |
| `INFO` | Boost state resumed from DB on startup |
| `INFO` | Boost state cleared — reverted externally while service was down |
| `INFO` | Email sent |
| `WARN` | Boost impossible — host saturated or insufficient capacity |
| `WARN` | Manual change detected — adopting new value as baseline |
| `WARN` | Email send failed |
| `WARN` | Container skipped — excluded by tag |
| `WARN` | cpulimit=0 (unlimited) — CPU saturation check skipped |
| `ERROR` | Proxmox API error |
| `ERROR` | Database error |
| `ERROR` | Failed to revert boost during shutdown |

### Example output

```
time=2026-03-27T14:30:00Z level=INFO msg="service started" version=1.0.9 node=pve poll_interval=5s
time=2026-03-27T14:30:00Z level=INFO msg="DB opened" path=/var/lib/proxmox-autoscaler/state.db status=existing
time=2026-03-27T14:30:00Z level=INFO msg="boost state resumed from DB on startup" vmid=102 resource=cpu original=4 boosted=6 remaining_seconds=73
time=2026-03-27T14:31:13Z level=INFO msg="boost reverted - normal" vmid=102 resource=cpu boosted_value=6 original_value=4 current_usage_pct=38.2
time=2026-03-27T14:31:13Z level=INFO msg="email sent" to=admin@example.com subject="Autoescalado revertido en MAIN => testing.local(102)"
time=2026-03-27T14:45:22Z level=INFO msg="boost applied" vmid=105 resource=memory original_value=2048 new_value=3072 factor=1.5
time=2026-03-27T14:47:10Z level=WARN msg="manual change detected - adopting new baseline" vmid=105 resource=memory expected_boost_value=3072 actual_value=4096 new_baseline=4096
time=2026-03-27T14:52:01Z level=WARN msg="boost impossible" vmid=107 resource=cpu reason="host CPU saturated (92% >= threshold 90%)"
```

## Service management

```bash
# View live logs
journalctl -u proxmox-autoscaler -f

# Restart after config change
systemctl restart proxmox-autoscaler

# Stop (reverts all active boosts before exiting)
systemctl stop proxmox-autoscaler

# Disable
systemctl disable proxmox-autoscaler
```

## Building from source

```bash
# For your local OS/arch
make build

# Cross-compile for Linux amd64 (from macOS or any OS)
make build-linux
```

Requires Go 1.21+. No CGO, no external system dependencies.

## License

The Proxmox ecosystem and the broader open-source community provide an enormous number of tools that we rely on every day. This project is our way of giving back — making something genuinely useful available to everyone who builds on top of Proxmox, without restrictions.

If this project saves you time or helps your infrastructure handle load better, consider contributing improvements or spreading the word.

❤️ Made with love by VGS for the open-source community.

MIT — Copyright (c) 2026 [VGS](https://vgst.net)
