# vps tunnel — design

Status: draft — awaiting review
Date: 2026-09-10

## Why

Connecting to a dev, staging, or production database is a daily task. Today it
runs through a 182-line bash script:

```bash
vps tunnel key_connect_backend api_mongo_staging 27017 27018
```

The script resolves the container's IP once, at startup, then execs
`ssh -N -L`. Three things follow from that:

1. **A container restart kills the tunnel permanently.** The container comes
   back on a new IP; the forward still points at the old one. The tunnel is
   dead and stays dead until it is noticed and restarted by hand.
2. **The terminal is held hostage.** One tab per open database, all day.
3. **Failures are silent and unclassified.** A typo'd hostname, a busy local
   port, and a genuine network blip all produce raw ssh stderr, and none of
   them are distinguishable at a glance.

This document specifies a Go replacement for `vps tunnel` that supervises each
tunnel, re-resolves the container address on every retry, and runs in the
background.

## Goals

- The existing invocation keeps working, byte for byte.
- A tunnel survives container restarts, laptop sleep, and network changes
  without manual intervention.
- Which environment a tunnel points at is obvious in every line of output.
- Fatal errors stop immediately with an actionable message; transient errors
  retry quietly.
- The retry logic, backoff timing, and error classification are testable with
  no VPS and no real sleeping.

## Non-goals

Explicitly out of scope for this version, listed so the boundary is not
relitigated during implementation:

- Named services / alias config files.
- Host inventory, `info`, `ps`, remote docker operations, file transfer.
- Reboot persistence via launchd. The state file reserves an `autostart`
  field so this can be added without rework, but nothing reads it yet.
- Provisioning, config management, cloud-provider APIs.
- Supervision, retry, or state for anything other than tunnels. The
  passthrough commands below are deliberately dumb `exec` wrappers.

## Command surface

```
vps tunnel <host> <container> <remote-port> <local-port> [flags]
vps tunnel ls
vps tunnel down <local-port> | --all
vps tunnel logs <local-port> [-f] [-n <count>]
vps tunnel restart <local-port>
```

Flags on the create form:

| Flag | Default | Meaning |
|---|---|---|
| `--attach`, `-a` | off | Stay in the foreground streaming state changes. `^C` stops the tunnel. |
| `--env <name>` | inferred | Environment label. Overrides inference. |
| `--wait <dur>` | `10s` | How long to block waiting for the first healthy state before returning. `0` returns immediately. |

Global: `--json`, `--no-color`, `--quiet`, `--version`, `--help`.

### Passthrough commands

The old script is to be deleted once this binary is trusted, so everything it
does must have a home. These four carry over unchanged in behaviour and
argument order, as direct `exec` handoffs to `ssh`/`scp`:

```
vps2 run <host> <command...>            exec ssh <host> <command...>
vps2 shell <host>                       exec ssh <host>
vps2 push [scp-opts...] <host> <local> <remote>
vps2 pull [scp-opts...] <host> <remote> <local>
```

They get no supervision, no retry, no daemon involvement, and no state. They
exist so that removing the bash script loses nothing. `docker-ip` also carries
over, since the tunnel supervisor already resolves container addresses:

```
vps2 docker-ip <host> <container>       print the container's IP
```

Anything beyond this — inventory, remote docker operations, rsync-backed
transfer — stays out of scope and is a later version's problem.

### Addressing tunnels

Tunnels are addressed by **local port**. It is already unique per tunnel, it is
the number that is typed into the database client anyway, and it requires
inventing no new naming scheme.

### Environment labelling

The environment is inferred from the container and host names by substring, in
this order: `prod`/`production` → `prod`, `stg`/`staging` → `stg`,
`dev`/`develop` → `dev`. No match yields an empty label. `--env` overrides.

`prod` renders in red, `stg` in yellow, `dev` in green, everywhere the tunnel
appears.

There is deliberately **no confirmation prompt for prod**. A prompt that fires
several times a day trains reflexive dismissal and protects nothing; the actual
failure mode is believing a `localhost:27018` session is staging when it is
production, and that is a display problem, not a consent problem.

### Output

```
$ vps tunnel example-backend-dev app_mongo_staging 27017 27018
  stg  app_mongo_staging  →  localhost:27018   healthy  (1.2s)

$ vps tunnel ls
LOCAL  ENV   HOST                      CONTAINER               REMOTE  STATE     SINCE   RETRIES
27018  stg   example-backend-dev   app_mongo_staging  27017   healthy   2h14m   3
27019  prod  example-backend-prod  app_mongo           27017   retrying  —       12
6380   dev   example-api       api_redis        6379    healthy   18m     0
```

`ls` verifies liveness at call time by dialling each local port, so `healthy`
means the forward actually accepts a connection — not merely that an ssh
process exists. A port that is listening but not answering shows as `degraded`,
and the probe failure that revealed it also queues that tunnel for a recycle,
so the next `ls` shows it reconnecting rather than reporting the same
degradation forever.

Colour is applied only when stdout is a TTY and `NO_COLOR` is unset.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 64 | Usage error (matches the current script's `EX_USAGE`) |
| 69 | Daemon unreachable and could not be started |
| 70 | Internal error |
| 75 | Tunnel did not reach healthy within `--wait` (it keeps retrying in the background) |
| 1 | Fatal tunnel error (auth, unknown host, port conflict) |

## Reliability model

This is the core of the work. Everything else is plumbing.

### Per-tunnel state machine

```
                ┌──────────────┐
   start  ──▶   │  resolving   │  ssh <host> docker inspect → container IP
                └──────┬───────┘
                       │ ok                  ┌──────────┐
                       ▼                     │  fatal   │ ◀── auth / unknown host /
                ┌──────────────┐             └──────────┘     port conflict / N unknowns
                │  connecting  │  spawn ssh -N -L
                └──────┬───────┘
                       │ forward bound
                       ▼
                ┌──────────────┐  `ls` probe fails  ┌──────────────┐
                │   healthy    │ ─────────────────▶ │   degraded   │
                └──────┬───────┘                    └──────┬───────┘
                       │ ssh exits                         │
                       ▼                                   │
                ┌──────────────┐                           │
                │   retrying   │ ◀─────────────────────────┘
                └──────┬───────┘
                       │ backoff elapsed
                       └────────────▶ resolving   (re-resolve, always)
```

The arrow back to `resolving` rather than `connecting` **is the fix for the
bug**. Every retry attempt re-runs `docker inspect`, so a container that came
back on a new IP is picked up automatically on the next attempt.

### Error classification

Retry decisions are made by matching ssh's stderr, never by exit code alone.

| Signal | Class | Action |
|---|---|---|
| `Permission denied`, `Too many authentication failures` | auth | fatal |
| `Host key verification failed` | auth | fatal |
| `Could not resolve hostname`, `Name or service not known` | config | fatal |
| `bind: Address already in use`, `cannot listen to port` | local | fatal |
| `Connection refused`, `Connection timed out`, `No route to host`, `Network is unreachable` | network | retry |
| `Connection closed by remote host`, `Broken pipe`, `Timeout, server ... not responding` | network | retry |
| `docker inspect` returns empty or `No such object` | container | retry |
| No stderr match | unknown | retry, fatal after 10 consecutive |

A missing container retries indefinitely rather than giving up on a timer — an
image pull can legitimately take minutes — but `ls` surfaces it explicitly as
`waiting: container not found (4m)` so it never looks like a hang.

### Backoff

Exponential from 1s, doubling, capped at 30s, with ±20% jitter. The counter
resets to 1s after 60 continuous seconds of `healthy`, so a flapping container
does not leave a tunnel stuck at the 30s ceiling once it stabilises.

### Liveness

Three independent mechanisms, because each covers a case the others miss:

1. **ssh keepalives.** Every forward is spawned with
   `-o ServerAliveInterval=15 -o ServerAliveCountMax=3`, so ssh exits within
   ~45s of a link that has gone away without closing. `-o ExitOnForwardFailure=yes`
   makes it exit immediately rather than sitting there with no forward when the
   bind fails.
2. **On-demand probe.** `vps tunnel ls` dials each local port to report truth
   rather than assumption. Probing is not done on a timer: a periodic dial
   against a database port produces a connection-open/close entry in that
   database's log every interval, forever, which is unacceptable noise on prod.
3. **Clock-jump detection.** The supervisor compares monotonic and wall-clock
   elapsed time each tick. A discrepancy above 30s means the machine slept;
   every tunnel is recycled proactively instead of waiting out the keepalive
   window. This is what makes closing and reopening the laptop lid work
   without a manual restart, and it needs no cgo.

Network changes (wifi → tethering → VPN) are caught by polling the default
route every 10s; a change recycles all tunnels immediately.

### Local port conflicts

Before spawning ssh, the supervisor binds the local port itself to test
availability, then releases it. If the bind fails, it identifies the holder via
`lsof -nP -iTCP:<port> -sTCP:LISTEN` and reports it by name and PID. If the
holder is another `vps` tunnel, the message says which host and container that
tunnel serves.

## Architecture

Five packages. The dependency rule that makes the whole thing testable:
**process spawning is confined to `internal/sshexec` and `internal/sysprobe`,
and both sit behind interfaces with fake implementations.** No other package
may call `exec.Command`.

| Package | Responsibility |
|---|---|
| `internal/sshexec` | Spawns `ssh`. Exposes an `Executor` interface and a `Fake` implementation. Owns ControlMaster sockets for command connections. |
| `internal/sysprobe` | Spawns `lsof` (who holds a local port) and `route` (current default gateway). Interface plus fake, for the same reason. |
| `internal/tunnel` | Per-tunnel state machine, classification, backoff. Pure logic over an injected `Executor` and `Clock`. No I/O of its own. |
| `internal/supervisor` | Owns the set of tunnels, the control socket, the state file, sleep/network detection. |
| `cmd/vps` | Argument parsing, output rendering, daemon auto-spawn. Thin. |

To verify the rule still holds:

```bash
grep -rn "exec.Command" --include='*.go' internal/ cmd/ \
  | grep -vE "^internal/(sshexec|sysprobe)/"
```

### Process model

The port forward itself is a plain `ssh -N -L` child process tracked by PID —
no ControlMaster, because a forward needs no out-of-band control channel and
the socket is one more thing to leak.

The short `docker inspect` command connections *do* use multiplexing, since
they run on every retry:

```
-o ControlMaster=auto -o ControlPath=<dir>/cmd-<hash>.sock -o ControlPersist=60
```

Socket paths have a hard length limit. `sun_path` is 104 bytes, and ssh binds
an intermediate socket with a 17-byte random suffix while establishing a
master, leaving a usable budget of 86. If `~/.vps/ctl/` would exceed it, the
socket directory falls back to `/tmp/vps-<uid>/`.

Children are spawned in the supervisor's own process group, and the group is
killed on shutdown. On startup the supervisor kills any orphaned ssh process
whose command line references its own socket directory, then reconciles from
the state file. Orphan detection matches the socket directory, not
`pgrep -f 'ssh -o ControlMaster'` — once `ControlPersist` backgrounds a master,
ssh renames the process to `ssh: <socket> [mux]` and that pattern reports clean
while masters are still alive.

### Daemon lifecycle

The supervisor is auto-spawned by the first `vps tunnel` that finds no running
daemon, and exits when its last tunnel is removed. There is nothing to install.

- Control socket: `~/.vps/ctl.sock`, newline-delimited JSON request/response.
  No RPC framework dependency.
- Spawn race: an exclusive `flock` on `~/.vps/daemon.lock` ensures that two
  concurrent `vps tunnel` invocations produce one daemon.
- Stale socket: a connect that fails while the lock is free means the previous
  daemon died; the socket is unlinked and a new daemon started.
- Daemon stdout/stderr go to `~/.vps/daemon.log`, size-rotated at 5 MB.

### Files

```
~/.vps/
├── state.json        tunnel specs + desired state, written atomically
├── ctl.sock          control socket
├── daemon.lock       spawn lock
├── daemon.log        supervisor log
├── ctl/              ControlMaster sockets for command connections
└── logs/<port>.log   per-tunnel event log, read by `vps tunnel logs`
```

`state.json` is written to a temp file and renamed, so a crash mid-write cannot
corrupt it. Each tunnel record carries an `autostart` boolean, reserved for
launchd support and currently always false.

Per-tunnel events are also kept in a 200-entry in-memory ring buffer so
`vps tunnel logs -f` can replay recent history before streaming.

## Testing

Test-driven throughout. `go test ./... -short` runs the entire suite
hermetically — no VPS, no network, no real sleeping.

- **Classification** — table-driven over captured real ssh stderr samples.
- **Backoff** — fake clock, asserting exact delay sequences including jitter
  bounds and the reset-after-healthy rule.
- **State machine** — fake `Executor` plus fake clock, covering: container
  restarts to a new IP, auth failure going fatal on the first attempt, the
  unknown-error cap, sleep-induced recycling, port conflict.
- **State file** — atomicity under concurrent writes, and recovery from a
  truncated file.
- **Control protocol** — request/response round trips over a socket in a temp
  directory, including stale-socket recovery and the spawn race.
- **End-to-end CLI** — a stub `ssh` executable injected on `PATH`, so the real
  argument construction and output rendering are exercised without a server.
- **Integration** — gated on `VPS_TEST_HOST`, skipped when unset:

  ```bash
  VPS_TEST_HOST=example-backend-dev \
  VPS_TEST_CONTAINER=app_mongo_staging \
  VPS_TEST_REMOTE_PORT=27017 \
  go test ./internal/sshexec/ -run Integration -v
  ```

## Dependencies

Go 1.25+. `github.com/spf13/cobra` for the command tree, help text, and the
completion scaffolding a later version will want. Everything else is standard
library: `text/tabwriter` for tables, a small internal ANSI helper for colour,
`encoding/json` for the control protocol and state file.

## Migration

The old script at `~/.local/bin/vps` is to be decommissioned, which is only
safe once every one of its six commands has an equivalent. It does, by the end
of this spec: `tunnel` is the supervised replacement, `docker-ip` falls out of
the supervisor, and `run`/`shell`/`push`/`pull` carry over as passthroughs.

The cutover runs in four steps, and the deletion is the last one:

1. **Install alongside.** The binary installs as `vps2`. The existing `vps`
   script is untouched, so there is always a working fallback one keystroke
   away. Every command in this document is typed as `vps2 …` during this stage.
2. **Burn in.** Use `vps2 tunnel` for daily dev, staging, and production
   database work for one week. The bar to advance is that no tunnel needed a
   manual restart across at least one container redeploy, one laptop
   sleep/wake cycle, and one network change.
3. **Verify the passthroughs.** Confirm `run`, `shell`, `push`, `pull`, and
   `docker-ip` behave identically to the script, including argument order,
   `scp` option forwarding, and exit codes.
4. **Decommission.** Copy the script to `docs/legacy-vps.sh` in this repo so
   its behaviour stays readable, delete `~/.local/bin/vps`, and install the
   binary as `vps`. Only then does the old name belong to the new tool.

Step 4 does not run on a schedule or as part of any build. It happens once,
deliberately, after steps 2 and 3 have actually passed.
