# hop

[![CI](https://github.com/thailoc-dev/hop/actions/workflows/ci.yml/badge.svg)](https://github.com/thailoc-dev/hop/actions/workflows/ci.yml)

Supervised SSH tunnels to Docker containers on remote hosts.

```bash
hop example-backend-dev app_mongo_staging 27017 27018
```

Forwards `localhost:27018` to port 27017 inside `app_mongo_staging` on
`example-backend-dev`, in the background, and keeps it alive across container
restarts, laptop sleep and network changes.

**Contents** — [Why](#why-it-exists) · [Install](#install) · [Usage](#usage) ·
[Completion](#tab-completion) · [How it works](#how-it-works) ·
[Development](#development)

## Why it exists

It replaces a bash script that resolved the container's IP once at startup.
When the container restarted it came back on a new address, the forward kept
pointing at the old one, and the tunnel was dead until someone noticed. `hop`
re-resolves the address on every retry attempt, so it comes back on its own.

It also survives what a laptop does all day: closing the lid, and moving
between wifi, tethering and a VPN. Neither closes the connection cleanly, so
ssh can take 45 seconds to notice — or never. `hop` watches for both and
rebuilds affected tunnels immediately.

## Install

**Requirements:** macOS and an `ssh` that reads `~/.ssh/config`. Building
needs Go 1.25; `cobra` is the only dependency.

### 1. Get Go

Pick one. The project pins Go 1.25.0 in `.mise.toml`, so `mise` picks the
right version automatically inside the repo:

```bash
# with mise (recommended — honours the pinned version)
brew install mise
echo 'eval "$(mise activate zsh)"' >> ~/.zshrc && exec zsh
cd hop && mise install        # installs Go 1.25.0 from .mise.toml

# or with Homebrew
brew install go

# or from https://go.dev/dl — the macOS .pkg installer
```

Check with `go version`; it must print 1.25 or newer. Then make sure
`~/.local/bin` is on your PATH, since that is where hop installs:

```bash
grep -q '.local/bin' ~/.zshrc || echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

### 2. Install hop

The short way, once Go is on your PATH:

```bash
go install github.com/thailoc-dev/hop/cmd/hop@latest
hop version
```

That puts the binary in `$(go env GOPATH)/bin` — usually `~/go/bin` — so
make sure that directory is on your PATH too. Or build from a clone, which
is what you want if you intend to change anything:

```bash
git clone https://github.com/thailoc-dev/hop.git
cd hop
make install          # builds ./bin/hop, installs to ~/.local/bin/hop
hop version
```

### 3. Tab completion (optional but recommended)

Completes hosts, containers, ports and saved tunnel names:

```bash
hop completion zsh > "${fpath[1]}/_hop"   # then restart the shell
```

`bash` and `fish` are also supported.

## Usage

### Open a tunnel

```
hop <host> <container> <remote-port> <local-port>
```

It returns as soon as the forward is up and keeps running in the background.
Add `--attach` to stay in the foreground streaming state changes, where `^C`
stops the tunnel.

| Flag | Default | Meaning |
|---|---|---|
| `--attach`, `-a` | off | Stay in the foreground; `^C` stops the tunnel |
| `--env <name>` | inferred | Environment label: `dev`, `stg`, `prod` |
| `--wait <dur>` | `10s` | How long to wait for the tunnel to become healthy |

### Save it under a name

```bash
hop example-tracker-dev tracker_redis_staging 6379 46379 --name redis-stg
# or, after opening it the long way:
hop save redis-stg            # names the most recently opened tunnel
hop save redis-stg 46379      # or a specific one

hop redis-stg                 # from now on
hop up redis-stg              # same thing, explicit
hop forget redis-stg          # remove the name; a running instance keeps running
```

Names are lower-case letters, digits, `-` and `_`, and must contain a
non-digit so they can never be mistaken for a port. Saving an existing name
overwrites it — that is how you change a saved tunnel's ports.

`down`, `logs` and `restart` accept a name wherever they accept a port, and
`hop ls` shows saved tunnels that are not running as `saved` rows.

### Manage tunnels

| Command | Does |
|---|---|
| `hop ls` | List tunnels, dialling each port to report the truth |
| `hop down <name\|port>` | Stop one tunnel |
| `hop down --all` | Stop every tunnel |
| `hop logs <name\|port> [-f]` | Show state transitions |
| `hop restart <name\|port>` | Rebuild it, re-resolving the container address |

Tunnels are addressed by **local port** — it is unique per tunnel, and it is
the number you type into your database client anyway.

### Passthrough commands

These are thin `ssh`/`scp` wrappers with no supervision, kept so that `hop`
covers everything the bash script it replaces did:

| Command | Equivalent |
|---|---|
| `hop run <host> <command...>` | `ssh <host> <command...>` |
| `hop shell <host>` | `ssh <host>` |
| `hop push [scp-opts] <host> <local> <remote>` | `scp <local> <host>:<remote>` |
| `hop pull [scp-opts] <host> <remote> <local>` | `scp <host>:<remote> <local>` |
| `hop docker-ip <host> <container>` | Print the container's IP |

If your first argument is one of the subcommand names above, it is read as a
subcommand. A host genuinely called `ls` is still reachable as `hop tunnel ls …`.

### Environments

The environment is inferred from the container and host names and shown in
every line of output: `prod` red, `stg` yellow, `dev` green. Override with
`--env`.

There is no confirmation prompt for prod on purpose. A prompt that fires
several times a day gets dismissed reflexively; the failure that actually
matters is believing a `localhost` session is staging when it is production,
and that is fixed by making it obvious, not by asking.

### Tab completion

Completion covers hosts (from `~/.ssh/config`), container names and their ports
(from `docker ps` on the host), and the local ports of running tunnels for
`down`, `logs` and `restart` — each annotated with its environment and
container.

**The first Tab on a host you have not completed recently shows a message
rather than containers:**

```
$ hop example-webapp-dev <TAB>
fetching containers on example-webapp-dev — press Tab again in a moment
```

A first ssh connection costs seconds — TCP, key exchange and authentication
before docker even runs — which no 300 ms ceiling can accommodate. That press
schedules a detached `hop __warm` to fetch the list out of band; press Tab
again a moment later and it is instant. Without the background fetch the cache
could never fill and completion would fail on every press forever.

The message matters as much as the mechanism: an empty completion list is
indistinguishable from a broken host, and gets reported as one.

Network-backed completions are bounded by a 300 ms timeout and cached for a
minute under `~/.hop/cache/`, so pressing Tab against an unreachable host
returns immediately rather than hanging the shell. On timeout an expired cache
entry is served in preference to nothing: a minute-old container list is almost
always still right, and being wrong costs one keystroke.

## How it works

The first `hop` command auto-spawns a background supervisor and talks to it
over `~/.hop/ctl.sock`. The supervisor exits when its last tunnel is stopped.
Nothing to install, and no launchd agent — tunnels do not come back after a
reboot.

A tunnel is `healthy` only once ssh has **bound the local port**, confirmed by
a bind test rather than a dial, so establishing a tunnel opens no connection to
your database.

```
~/.hop/
├── state.json      running tunnels, written atomically
├── tunnels.json    saved, named tunnels (the CLI's; the daemon never reads it)
├── ctl.sock        control socket
├── daemon.lock     spawn lock
├── daemon.log      supervisor log, rotated at 5 MB
├── ctl/            ControlMaster sockets for command connections
└── cache/          container listings for completion
```

`~/.ssh/config` is read by ssh itself and never written to. Because hop simply
invokes `ssh <host>`, everything configured there applies — including
`ProxyJump` for hosts behind a bastion.

## Development

### Layout

| Path | Responsibility |
|---|---|
| `internal/sshexec` | Spawns ssh. `Executor` interface plus a fake. |
| `internal/sysprobe` | Spawns lsof and route, and signals orphans. Interface plus a fake. |
| `internal/tunnel` | State machine, classification, backoff. Pure logic. |
| `internal/store` | The state file. |
| `internal/control` | Client and server for the control socket. |
| `internal/supervisor` | The tunnel set, orphan reaping, sleep and network watching. |
| `internal/hopfs` | Every path hop reads or writes. |
| `internal/sshconfig` | Read-only `~/.ssh/config` alias parser, for completion. |
| `internal/complete` | Completion data: `docker ps` parsing, the timeout, the cache. |
| `cmd/hop` | Argument parsing, rendering, daemon spawn. |

Process spawning is confined to `sshexec`, `sysprobe`, and `cmd/hop/connect.go`
(which re-executes hop itself). That rule is what makes retry timing and error
classification testable without a VPS. `make check-spawn` enforces it, over
production code only — test files may spawn, which is how the end-to-end test
drives the real binary.

### Build and check

```bash
make build      # ./bin/hop
make install    # ~/.local/bin/hop
make check      # gofmt, spawn rule, vet, race tests, end-to-end
```

**After changing anything, reinstall.** `make build` writes `./bin/hop`; the
binary on your PATH is `~/.local/bin/hop` and does not update itself. A stale
one has been mistaken for a host-specific bug more than once:

```bash
shasum ~/.local/bin/hop bin/hop   # the two hashes must match
```

### Testing

```bash
go test ./... -short                    # hermetic: no network, no VPS, no sleeping
go test ./cmd/hop/ -run TestEndToEnd    # spawns a real daemon against a stub ssh
```

Integration tests need a reachable host and skip without one:

```bash
HOP_TEST_HOST=example-backend-dev \
HOP_TEST_CONTAINER=app_mongo_staging \
HOP_TEST_REMOTE_PORT=27017 \
go test ./internal/sshexec/ -run Integration -v
```

CI runs `make check` on macOS for every push and pull request — macOS rather
than Linux because `sysprobe` shells out to `route -n get default`, which is
BSD-only, and `hopfs` derives its socket budget from darwin's 104-byte
`sun_path`. A green Ubuntu run would be testing a platform hop does not support.

CI does **not** run the integration tests: they need a reachable VPS. That gap
is deliberate but worth knowing, because it is exactly where three bugs hid —
the fakes cannot exercise ssh's argument handling.

### Two traps this suite has fallen into

**Socket paths.** Never build one from `t.TempDir()`: it embeds the test name,
and a long one pushes past the 104-byte `sun_path` limit. `shortTempDir` uses
`/tmp` directly rather than `TMPDIR`, which on macOS is itself ~48 bytes —
enough on its own to break a ControlMaster path once ssh appends its 17-byte
suffix.

**Stubs that encode the bug.** A stub `ssh` matching on a bare `inspect`
argument passes against a caller that forgets to quote the remote command,
because ssh joins its argv and the remote shell re-parses it. Stubs must match
what ssh really receives: one shell-quoted string.

### Documents

| Document | Path |
|---|---|
| Design | `docs/superpowers/specs/2026-09-10-hop-design.md` |
| Implementation plan | `docs/superpowers/plans/2026-09-10-hop.md` |
| Decommissioning the old script | `docs/decommission-checklist.md` |
