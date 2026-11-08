---
title: Migrating from 9Router
order: 6
section: Getting Started
---

# Migrating from 9Router

The SQLite data already carries over; replacing the running process and its
hidden autostart is a separate step:

```sh
9routerx service take-over
```

The command uses the configured `PORT` (default 20128) and `DATA_DIR`. It reports
the listener's fingerprint, PID, executable, command line and parent PID, asks
before stopping it, waits for the port, inventories matching legacy autostart,
asks before disabling each entry, and calls the **same service backend as
`service install`**. It then checks `/health` and the 9RouterX root fingerprint
and prints read-only counts of `providerConnections` and `apiKeys`, the database
path, dashboard URL and login guidance. Zero rows warn about a different
`DATA_DIR`; they do not mean migration erased anything.

Every action is reported. Force-kill is attempted only after graceful stop
fails, with an explicit warning and another confirmation. Without a terminal,
confirmation fails instead of hanging. For reviewed unattended migrations:

```sh
9routerx service take-over --yes
```

`--yes` approves **all** reported actions, including the warned force fallback.
It does not bypass process identity or legacy path matching. Take a database
backup first; see [Database](database.md). Windows service installation may need
an Administrator terminal; a failure is reported, not presented as success.

## The real problem this solves

An operator installed 9RouterX while an autostarted Node.js/Next.js 9Router kept
returning after every restart. Task Scheduler, the Startup folder, Task Manager
Startup apps and Registry Run keys all appeared empty. There was no obvious
process to stop or useful error: both gateways competed for port 20128, or the
operator gave up. Data compatibility alone did not solve process migration.

If no matching autostart is found, take-over prints the old command line and
**parent PID**, warns that the parent may respawn it, and asks before continuing.
Disable that parent and rerun take-over, or keep the old gateway and use the
next port. A respawn occupying the port aborts installation instead of silently
claiming success. HTTP fingerprints vary across upstream versions, redirects
and custom themes; executable/command-line inspection is the fallback, not a
claim that every Next.js page identifies 9Router.

Upstream's shutdown endpoint can terminate multiple matching processes and
`cloudflared`, so take-over deliberately does **not** call that broad legacy
shutdown helper. It requests a stop of the verified listener PID only. For a
9RouterX daemon with a valid pidfile it reuses authenticated graceful HTTP shutdown.

## First run and side-by-side operation

When `9routerx start` has no pidfile in this `DATA_DIR` and the port is occupied,
a terminal shows:

1. Take over the existing gateway (`service take-over`).
2. Use another free port (recommended default 20129).
3. Abort (default).

With piped/non-TTY stdin it exits with an informative error; no automatic
choice and no prompt. Identified 9Router/9RouterX conflicts name take-over;
unrelated listeners only get the alternative-port hint. Later starts use the
same conflict description, without repeating the first-run menu.

```sh
# macOS / Linux
PORT=20129 9routerx start
PORT=20129 9routerx service install
```

```powershell
# Windows PowerShell
$env:PORT = '20129'
9routerx start
9routerx service install
```

The service captures the effective port and absolute data/database paths in
its command arguments so the next login does not depend on your shell's env.
Log in with your existing dashboard password. For a new empty database use
`INITIAL_PASSWORD` as described in [Installation](installation.md).

## Windows: where autostart can hide

These are **read-only** inspections. Run them in PowerShell. Compare paths with
the listener, not just the shared `node.exe` interpreter:

```powershell
# Listener and its identity (replace 20128 if PORT differs)
netstat -ano -p tcp | Select-String ':20128\s+.*LISTENING'
# Substitute the PID from the final netstat column, not PowerShell's reserved $PID
$gatewayPid = 1234
Get-CimInstance Win32_Process -Filter "ProcessId = $gatewayPid" |
  Select-Object ProcessId,ParentProcessId,Name,ExecutablePath,CommandLine

# Task Scheduler: exact commands, including subfolders
schtasks /query /fo LIST /v | Select-String '9router|node' -Context 3,3
Get-ScheduledTask | Select-Object TaskPath,TaskName,Actions

# Registry Run keys (HKLM is inspection only; only migrate it with proven system scope)
reg query 'HKCU\Software\Microsoft\Windows\CurrentVersion\Run'
reg query 'HKLM\Software\Microsoft\Windows\CurrentVersion\Run'

# Current user's Startup folder; inspect .lnk targets too
Get-ChildItem ([Environment]::GetFolderPath('Startup'))
$ws = New-Object -ComObject WScript.Shell
Get-ChildItem ([Environment]::GetFolderPath('Startup')) -Filter *.lnk |
  ForEach-Object { $ws.CreateShortcut($_.FullName) } |
  Select-Object TargetPath,Arguments

# Task Manager > Startup apps; services.msc for system services
Get-CimInstance Win32_StartupCommand | Select-Object Name,Command,Location,User
Get-CimInstance Win32_Service |
  Where-Object { $_.PathName -match '9router|node' } |
  Select-Object Name,ProcessId,StartMode,PathName

# Only if installed: pm2 can resurrect apps from its saved process list
pm2 jlist
```

Take-over inventories tasks via their XML/CIM actions (locale-independent),
HKCU Run, Startup files/shortcut targets and Windows service paths. HKLM Run
is considered only when the old listener was proven to be a Windows service.
It disables matching tasks/services, removes confirmed Run values with a
registry warning, and renames matching Startup files with `.9router-disabled`.
It never matches generic `node.exe` alone or disables 9RouterX entries. An entry
whose command changed since inspection is refused. Multi-action tasks and
compound/multiline commands are left for manual inspection so another app is
not disabled with the gateway. Wrappers with no exact legacy gateway path
match are reported as unresolved rather than guessed.

## macOS: launchd and Login Items

```sh
lsof -nP -iTCP:20128 -sTCP:LISTEN
ps -p 1234 -o pid=,ppid=,comm=,args=
launchctl list | grep -i router
find "$HOME/Library/LaunchAgents" -name '*.plist' -print
# Replace with a plist from that list; inspect Program/ProgramArguments
plutil -p "$HOME/Library/LaunchAgents/com.example.9router.plist"
# Login Items/background items (inspect System Settings > General > Login Items too)
sfltool dumpbtm
pm2 jlist
```

Take-over checks user LaunchAgents' parsed Program/ProgramArguments. Matching
agents are unloaded and renamed `.9router-disabled`. Login Items managed by
another app may require disabling that app manually.

## Linux: systemd user, cron, desktop autostart

```sh
lsof -nP -iTCP:20128 -sTCP:LISTEN
# Alternative when lsof is unavailable; PID visibility may need permission
ss -ltnp 'sport = :20128'
ps -p 1234 -o pid=,ppid=,comm=,args=
systemctl --user list-unit-files --no-pager | grep -i router
systemctl --user show 9router.service --property=ExecStart --value
crontab -l | grep -E '^[[:space:]]*@reboot[[:space:]]'
find "$HOME/.config/autostart" -name '*.desktop' -print
# Inspect the listed files' Exec= lines before changing anything
grep -H '^Exec=' "$HOME"/.config/autostart/*.desktop
pm2 jlist
```

Take-over disables matching systemd user units, comments out matching `@reboot`
lines while preserving the rest of crontab, and renames matching desktop entries.
On every OS, matching pm2 apps are deleted by their numeric ID and `pm2 save`
updates resurrection state. If pm2 is not installed, it is skipped.

## Nothing found, but the old process returns

Inspect the reported parent before stopping anything else. Examples:

```powershell
Get-CimInstance Win32_Process -Filter 'ProcessId = 5678' |
  Select-Object ProcessId,ParentProcessId,ExecutablePath,CommandLine
```

```sh
ps -p 5678 -o pid=,ppid=,comm=,args=
```

A terminal manager, npm wrapper, process manager, container or other parent can
restart the gateway without a conventional Startup entry. Disable only the
parent you identify, then rerun take-over. Otherwise choose another port; do
not kill every Node.js process or delete unrelated autostart entries.
