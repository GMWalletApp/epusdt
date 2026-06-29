# epctl Installation and Verification Scripts

`epctl` is the repo-root Linux binary installer and service manager for released `epusdt` binaries on GitHub Releases.
`epctl-docker-test.sh` is the matching end-to-end validation script. It launches Ubuntu + systemd in Docker and verifies download, install, startup, upgrade, and init-password behavior with real release artifacts.

## Scope

- Linux only
- Binary installation only
- Release source is fixed to `https://github.com/GMWalletApp/epusdt/releases`
- Service management is based on systemd

## Dependencies and Privileges

`epctl` expects these commands:

- `curl`
- `tar`
- `systemctl`
- `install`
- `grep`
- `sed`

Notes:

- `install`, `upgrade`, and `self-install` write into `/opt`, `/etc/systemd`, and `/usr/local/bin`
- `status` and `logs` automatically re-run through `sudo` when needed
- in practice, the operator should have `sudo` access

## Fixed Paths

| Item | Path |
|------|------|
| Install directory | `/opt/epusdt` |
| Main binary | `/opt/epusdt/epusdt` |
| Active config | `/opt/epusdt/.env` |
| Example config | `/opt/epusdt/.env.example` |
| Extracted frontend assets | `/opt/epusdt/www` |
| Download cache | `/tmp/epusdt/<tag>/` |
| systemd unit | `/etc/systemd/system/epusdt.service` |
| Global epctl path | `/usr/local/bin/epctl` |

## Quick Start

Run the interactive menu from the repo root:

```bash
./epctl
```

The default interactive language is Chinese. You can force either language:

```bash
./epctl zh
./epctl en
./epctl --lang zh help
./epctl --lang en help
```

Install the script into PATH:

```bash
./epctl self-install
epctl
```

## Common Commands

Download a specific release:

```bash
./epctl download --tag v1.0.8
```

Install the service:

```bash
./epctl install --tag v1.0.8 \
  --app-uri https://pay.example.com \
  --listen 127.0.0.1:18000
```

Upgrade to a newer release:

```bash
./epctl upgrade --tag v1.0.9
```

Running `./epctl upgrade --tag ...` directly restarts `epusdt` immediately after the files are replaced.
If you want to replace files only, pass `--no-restart`.
If you want an explicit confirmation step, pass `--prompt-restart`; in an interactive terminal the prompt is `[Y/n]`, so pressing Enter restarts by default.

Inspect config, status, and logs:

```bash
./epctl show-config
./epctl status
./epctl logs --lines 200
```

Request the initial admin password:

```bash
./epctl init-password
```

## Behavior When `--tag` Is Omitted

For `download`, `install`, and `upgrade`, `epctl` resolves the current latest GitHub release tag first, then shows the exact tag and asks for confirmation.

Example:

```bash
./epctl install --app-uri https://pay.example.com
```

In interactive use, the script will display the resolved latest tag before continuing.
For automation, passing `--tag` explicitly is preferred. If you intentionally want to skip the confirmation, use:

```bash
EPCTL_ASSUME_YES=1 ./epctl download
```

## What Happens on First Install

`install` performs these steps:

1. Detect the current CPU architecture and download the matching GitHub Release archive
2. Extract into `/tmp/epusdt/<tag>/extract/`
3. Install the binary to `/opt/epusdt/epusdt`
4. Install `.env.example` to `/opt/epusdt/.env.example`
5. Create the system user and group `epusdt`
6. Auto-create `/opt/epusdt/.env` from `.env.example` if it does not exist
7. Write and enable `epusdt.service`

When `.env` is auto-created, the script applies only the minimum bootstrap changes:

- `install=false`
- `app_uri=<--app-uri, default http://127.0.0.1:8000>`
- `http_listen=<--listen, default 127.0.0.1:8000>`

If `/opt/epusdt/.env` already exists, install and upgrade keep it unchanged.
`/opt/epusdt/.env.example` is refreshed from the current release on every install / upgrade.

## What Happens During Upgrade

`upgrade` performs these steps:

1. Detect the current CPU architecture and download the target GitHub Release archive
2. Extract into `/tmp/epusdt/<tag>/extract/`
3. Require the existing `/opt/epusdt/.env`; if it is missing, the command fails and tells you to run `install` first
4. Replace `/opt/epusdt/epusdt`
5. Replace `/opt/epusdt/.env.example`
6. Keep the existing `/opt/epusdt/.env`
7. Refresh `epusdt.service` and run `systemctl daemon-reload`
8. Restart `epusdt` immediately by default

Additional behavior:

- `upgrade` no longer creates `.env` and no longer runs `systemctl enable`
- `upgrade --no-restart` replaces files only and prints a manual restart warning
- `upgrade --prompt-restart` asks whether to restart when an interactive terminal is available
- if the restart fails after an upgrade, the script attempts to roll back the previous binary, `.env.example`, and unit file

## systemd Service Details

The service name is fixed to `epusdt.service` and uses these core settings:

```ini
WorkingDirectory=/opt/epusdt
ExecStart=/opt/epusdt/epusdt http start
User=epusdt
Group=epusdt
Restart=always
RestartSec=3
```

The working directory stays at `/opt/epusdt` because the program extracts its `www/` assets next to the binary.

## What `init-password` Does

`epctl init-password` only calls the local HTTP route:

```text
GET /admin/api/v1/auth/init-password
```

It does not read the database directly.

The script reads `http_listen` from `/opt/epusdt/.env` and normalizes these listen values into a local request target:

- `:8000` -> `127.0.0.1:8000`
- `0.0.0.0:8000` -> `127.0.0.1:8000`

If the API returns `10040`, the bootstrap plaintext password is no longer available. Common reasons are:

- the admin password has already been changed
- the initial password has already been consumed and cannot be fetched again

In that case, `epctl` prints the original HTTP error body for diagnosis.

## Docker Validation Script

The repo also provides:

```bash
./epctl-docker-test.sh <install-tag> [upgrade-tag]
```

Examples:

```bash
./epctl-docker-test.sh v1.0.6
./epctl-docker-test.sh --lang en v1.0.6 v1.0.8
```

It performs these checks on the local machine:

- starts directly from `ubuntu:24.04` and installs systemd plus the test dependencies during container bootstrap
- starts a privileged container
- runs `epctl self-install` inside the container
- downloads real GitHub Release artifacts
- installs `epusdt`
- when `upgrade-tag` is provided, verifies `upgrade --no-restart`, the default non-interactive `upgrade`, and both the `n` / Enter branches of `upgrade --prompt-restart`
- verifies the systemd service, `www/index.html`, config output, logs, and status
- verifies that `init-password` succeeds once and later returns `10040` after the admin password is changed

Requirements:

- Docker installed locally
- permission to run Docker
- outbound access to GitHub Releases

## Recommendations

- Prefer explicit `--tag` values in automation
- After installation, run `./epctl show-config` once to verify the active `.env`
- Change the admin password immediately after retrieving the initial password
- If you want to validate the installer itself, run `./epctl-docker-test.sh` first
