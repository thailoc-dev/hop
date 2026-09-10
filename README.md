# hop

Supervised SSH tunnels to Docker containers on remote hosts.

```bash
hop example-backend-dev app_mongo_staging 27017 27018
```

Forwards `localhost:27018` to port 27017 inside `app_mongo_staging` on
`example-backend-dev`, in the background, and keeps it alive.

## Why it exists

It replaces a bash script that resolved the container's IP once at startup.
When the container restarted it came back on a new address, the forward kept
pointing at the old one, and the tunnel was dead until someone noticed. `hop`
re-resolves the address on every retry attempt, so it comes back on its own.

It also survives what a laptop does all day: closing the lid, and moving
between wifi, tethering and a VPN. Neither closes the connection cleanly, so
ssh can take 45 seconds to notice — or never. `hop` watches for both and
rebuilds affected tunnels immediately.

## Commands

```
hop <host> <container> <remote-port> <local-port>   open a tunnel
hop ls                                              list tunnels
hop down <local-port> | --all                       stop
hop logs <local-port> [-f]                          state transitions
hop restart <local-port>                            rebuild, re-resolving

hop run <host> <command...>                         ssh passthrough
hop shell <host>
hop push [scp-options...] <host> <local> <remote>
hop pull [scp-options...] <host> <remote> <local>
hop docker-ip <host> <container>
```

Tunnels are addressed by local port — it is unique per tunnel and it is the
number you type into your database client anyway.

If your first argument is one of the subcommand names above, it is read as a
subcommand. A host genuinely called `ls` is reachable as `hop tunnel ls …`.

Add `--attach` to stay in the foreground and stream state changes; `^C` then
stops the tunnel.

## Environments

The environment is inferred from the container and host names and shown in
every line of output: `prod` red, `stg` yellow, `dev` green. Override with
`--env`.

There is no confirmation prompt for prod on purpose. A prompt that fires
several times a day gets dismissed reflexively; the failure that actually
matters is believing a `localhost` session is staging when it is production,
and that is fixed by making it obvious, not by asking.

## Completion

```bash
hop completion zsh > "${fpath[1]}/_hop"   # then restart the shell
```

`bash` and `fish` are also supported. Completion covers hosts (from
`~/.ssh/config`), container names and their ports (from `docker ps` on the
host), and the local ports of running tunnels for `down`, `logs` and
`restart` — each annotated with its environment and container.

Network-backed completions are bounded by a 300 ms timeout and cached for a
minute under `~/.hop/cache/`, so pressing Tab against an unreachable host
returns immediately with nothing rather than hanging the shell. On timeout an
expired cache entry is served in preference to nothing: a minute-old container
list is almost always still right, and being wrong costs one keystroke.

## How it works

The first `hop` command auto-spawns a background supervisor and talks to it
over `~/.hop/ctl.sock`. The supervisor exits when its last tunnel is stopped.
Nothing to install, and no launchd agent — tunnels do not come back after a
reboot.

```
~/.hop/
├── state.json      tunnel definitions, written atomically
├── ctl.sock        control socket
├── daemon.lock     spawn lock
├── daemon.log      supervisor log, rotated at 5 MB
├── ctl/            ControlMaster sockets for command connections
└── cache/          container listings for completion
```

`~/.ssh/config` is read by ssh itself and never written to. Because hop simply
invokes `ssh <host>`, everything configured there applies — including
`ProxyJump` for hosts behind a bastion.

## Build

```bash
make build      # ./bin/hop
make install    # ~/.local/bin/hop
make check      # vet, race tests, spawn-rule check, end-to-end
```

Go 1.25, pinned in `.mise.toml`. `cobra` is the only dependency.

## Layout

| Path | Responsibility |
|---|---|
| `internal/sshexec` | Spawns ssh. `Executor` interface plus a fake. |
| `internal/sysprobe` | Spawns lsof and route, and signals orphans. Interface plus a fake. |
| `internal/tunnel` | State machine, classification, backoff. Pure logic. |
| `internal/store` | The state file. |
| `internal/control` | Client and server for the control socket. |
| `internal/supervisor` | The tunnel set, orphan reaping, sleep and network watching. |
| `internal/hopfs` | Every path hop reads or writes. |
| `cmd/hop` | Argument parsing, rendering, daemon spawn. |

Process spawning is confined to `sshexec`, `sysprobe`, and `cmd/hop/connect.go`
(which re-executes hop itself). That rule is what makes retry timing and error
classification testable without a VPS. `make check-spawn` enforces it, over
production code only — test files may spawn, which is how the end-to-end test
drives the real binary.

## Testing

```bash
go test ./... -short                    # hermetic: no network, no VPS, no sleeping
go test ./cmd/hop/ -run TestEndToEnd    # spawns a real daemon against a stub ssh
```

Tests must not build socket paths from `t.TempDir()`: it embeds the test name,
and a long one pushes the path past the 104-byte `sun_path` limit. Use the
`shortTempDir` helper instead.

Integration tests need a reachable host and skip without one:

```bash
HOP_TEST_HOST=example-backend-dev \
HOP_TEST_CONTAINER=app_mongo_staging \
HOP_TEST_REMOTE_PORT=27017 \
go test ./internal/sshexec/ -run Integration -v
```

## Design and plan

| Document | Path |
|---|---|
| Design | `docs/superpowers/specs/2026-09-10-hop-design.md` |
| Plan | `docs/superpowers/plans/2026-09-10-hop.md` |
| Decommissioning the old script | `docs/decommission-checklist.md` |
