---
title: Installation
order: 2
section: Getting Started
---

# Installation

## npm (primary)

```bash
npm install -g 9routerx
```

The launcher installs the right binary for your OS automatically (Windows, macOS Intel/Apple Silicon, Linux x64/ARM64). Requires Node.js **>= 18**; the gateway binary itself is a single Go executable with no runtime dependencies.

## Alternative — script installer (same binaries)

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/yanstd888/9routerx/main/install.sh | bash
```

```powershell
# Windows (PowerShell) — writes to %LOCALAPPDATA% + user PATH, no admin needed
irm https://raw.githubusercontent.com/yanstd888/9routerx/main/install.ps1 | iex
```

## Defaults

No flags required:

| | Default |
|---|---|
| Port | `888` |
| Data | `~/.9router` (`%APPDATA%/9router` on Windows) |
| Dashboard | `http://localhost:888` |
| Initial password | `123456` (localhost only) |

## Running

```bash
9routerx              # run in the foreground
curl http://localhost:888/health
9routerx version
```

Override only when you need something different (`PORT`, `DATA_DIR`/`DB_PATH` — there are no `--port` flags):

```bash
PORT=20129 9routerx
DATA_DIR=/srv/9router 9routerx
DB_PATH=/srv/9router/data.sqlite 9routerx
HOST=127.0.0.1 9routerx
```

## Background mode

```bash
9routerx start          # same as: 9RouterX --background   (or -d)
9routerx status         # pid, dashboard, log
9routerx restart
9routerx logs -n 100
9routerx stop
```

The detached process records itself in `DATA_DIR/run/gateway.pid` and writes its
output to `DATA_DIR/run/gateway.log`. A second `start` while one is running
refuses instead of fighting over the port.

> Windows stops the process with `TerminateProcess` instead of `SIGTERM`, so
> `stop` skips the graceful drain; use the dashboard's Shutdown button or
> `9routerx restart` when you want in-flight streams to finish first.

## Start on boot

```bash
9routerx service install     # systemd user unit, launchd agent, OpenRC/runit service, cron @reboot, or Windows logon task
9routerx service status
9routerx service start
9routerx service restart
9routerx service stop
9routerx service uninstall
```

`install` picks the native backend for the host. systemd, launchd, and cron all
install into the **user** scope and need no root; runit's `~/service` path too.
Two exceptions: OpenRC is a system service and needs root (run with `sudo`, or
pick `--backend cron`), and on Windows install registers a scheduled task via
`schtasks`, which requires an Administrator console. Force a backend with
`--backend systemd|launchd|openrc|runit|cron`.

## Docker

```bash
docker run -d --name 9routerx --restart unless-stopped \
  -p 888:888 -v "$HOME/.9router:/data" \
  -e PORT=888 -e DATA_DIR=/data \
  -e INITIAL_PASSWORD=your-secure-password \
  yanstd888/9routerx:latest
```

Experimental builds are marked **Pre-release** and never offered to a normal
install; Docker Hub tags them `exp` (`yanstd888/9routerx:exp`), never `latest`.

## Building from source

Prerequisites: Go 1.27 and Bun 1.x (the dashboard is embedded into the binary, so build web first).

```bash
git clone https://github.com/yanstd888/9routerx.git
cd 9RouterX
make web-build   # bun install --frozen-lockfile && bun run build
make build       # embeds VERSION into the Go binary
```

## First login

- **Localhost (`localhost` / `127.0.0.1`)**: the initial compatibility password is
  `123456`. Once logged in, change it in **Settings → Profile**.
- **Remote / VPS / Docker / LAN**: for security (preventing public takeover of
  fresh installs with known defaults), remote access blocks the default `123456`
  password. Either:
  1. Set `INITIAL_PASSWORD` on launch (recommended):
     ```bash
     INITIAL_PASSWORD="your-secure-password" 9routerx
     ```
  2. Or access via SSH port-forwarding first:
     ```bash
     ssh -L 888:127.0.0.1:888 user@remote-host
     # open http://localhost:888, log in with 123456, then change the password
     ```
