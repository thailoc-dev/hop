# Interactive picker — design

Status: draft — awaiting review
Date: 2026-09-20
Builds on: `2026-09-10-hop-design.md`, `2026-09-11-named-tunnels-design.md`

## Why

Opening a tunnel today needs either four arguments typed from memory, or a
saved name typed from memory, or tab completion — which costs two presses on
a cold host. All of it assumes you remember what exists.

`hop` on its own should show you what exists and let you pick:

```
$ hop
  Tunnels                                          type to filter…
› redis-stg    stg   tracker_redis_staging   :46379   saved
  mongo-dev    dev   app_mongo_staging       :27018   healthy
  ── hosts ──
  example-backend-dev
  example-tracker-dev

  ↑↓ move   enter open   esc quit
```

Two keystrokes — `hop`, `Enter` — opens the tunnel you use most.

## Goals

- `hop` with no arguments, on a terminal, opens the picker.
- Picking a saved tunnel opens it in two keystrokes.
- Picking a host walks through container and ports without leaving the
  picker, then opens.
- Every stage is unit-testable without a terminal.
- Scripts and pipes are unaffected: no terminal, no picker.

## Non-goals

- Managing running tunnels from the picker (stop, restart, rm). The picker
  opens things; management stays in commands. Revisit after real use.
- A persistent dashboard or `ls --watch`.
- Replacing tab completion. Both exist; they serve different habits.
- Mouse support.

## Dependency

`github.com/charmbracelet/bubbletea`, with `bubbles` (list, textinput,
spinner) and `lipgloss`. This amends the original spec's "cobra is the only
dependency" rule, which was a guard against pulling libraries for trivial
work. A terminal UI — raw mode, key parsing, redraw, resize, restoring the
terminal on `^C` — is exactly where a framework earns its place, and Bubble
Tea's model-update-view shape is what makes the picker testable at all.

The rule becomes: **no new dependency without a spec that says why.**

## Behaviour

### Entry

`hop` with no arguments:

- stdin and stdout are a terminal → open the picker;
- otherwise → print help, as today. `hop | cat`, cron, and scripts see no
  change.

`hop --help` still prints help regardless.

### Stage 1 — what to open

A single filterable list, in this order:

1. **Saved tunnels**, from the catalogue merged with the running list (the
   same `mergeSaved` view `hop ls` uses): name, env (coloured as everywhere
   else), container, `:local-port`, state.
2. A divider.
3. **Hosts**, from `~/.ssh/config`, read-only, patterns excluded — the same
   source completion uses.

Typing filters both groups by fuzzy match. `Enter` on a saved tunnel opens
it and the picker exits; on a running one it prints `already running on N`
and exits 0. `Enter` on a host advances to stage 2. `Esc` or `^C` quits with
exit 0 and nothing opened.

An empty catalogue and an empty ssh config is not an error: the list shows
one line saying so, and `Esc` quits.

### Stage 2 — container

Containers on the chosen host, fetched with `docker ps` through the same
`complete.Fetch` the background warmer uses, shown with a spinner and the
message `fetching containers on <host>…`. The ceiling is **10 seconds**, not
completion's 300 ms: this is interactive, the user is watching, and a first
connection costs seconds. The fetch writes the completion cache, so a later
Tab on this host is warm too. A warm cache is served instantly.

On timeout or error the stage shows the reason and lets the user type a
container name by hand — the host may be reachable but slow, or docker may
need a moment. `Esc` returns to stage 1.

### Stage 3 — remote port

The chosen container's ports as choices, from the same `docker ps` output.
A container with one port preselects it; with none, or if the user types,
any number 1–65535 is accepted. `Esc` returns to stage 2.

### Stage 4 — local port and name

A text input for the local port, prefilled with the remote port. Typing
replaces it. `Tab` fills a free port chosen by the OS (bind `127.0.0.1:0`,
read it back, release it) for when the default is taken. A busy port is
reported inline, not after the fact.

Then an optional name, validated with the same rules as `--name`; `Enter` on
an empty field skips it.

`Enter` opens the tunnel through **the same code path as the four-argument
form** — `openTunnel` with the assembled spec and flags — so the health wait,
the `--name` save, the environment inference and the printed open line are
identical to typing it out.

### After

The picker exits before the open line is printed, so the terminal is back
to normal and the output looks exactly like `hop <host> <container> <r> <l>`.
Exit codes are the open's exit codes.

## Architecture

| Unit | Responsibility |
|---|---|
| `internal/picker` | The Bubble Tea model: stages, filtering, key handling, view. Pure over injected data sources; no I/O of its own. |
| `internal/picker/sources.go` | The data the model needs, as an interface: saved tunnels, hosts, containers-for-host (with a context), free-port. One real implementation in `cmd/hop`, one fake in tests. |
| `cmd/hop/pick.go` | Wires sources to the real catalogue, ssh config, executor and cache; runs the program; hands the result to `openTunnel`. Decides TTY-or-help. |

The model's `Update` takes messages and returns a new model, so a test drives
it with `tea.KeyMsg` values and asserts on state — no terminal, no goroutines.
The container fetch is a `tea.Cmd` that returns a message; the fake source
returns it synchronously.

The process-spawning rule is unchanged: the picker spawns nothing. Its
container fetch goes through `sshexec.Executor`.

## Result type

```go
type Result struct {
    Spec  tunnel.Spec // fully assembled: host, container, both ports, name, env
    Named bool        // whether stage 1 picked a saved tunnel (open by name)
}
```

`cmd/hop` turns `Named` into `openSaved(name)` and the rest into
`openTunnel(spec)`.

## Testing

Hermetic, as always:

- Stage 1: list order (saved, divider, hosts); filter narrows both groups;
  `Enter` on saved yields `Named`; `Enter` on host advances; `Esc` quits.
- Stage 2: spinner while fetching; containers listed on success; error text
  and manual entry on failure; `Esc` goes back.
- Stage 3: one port preselected; several listed; typed port validated.
- Stage 4: prefilled local port; `Tab` fills a free port; busy port reported;
  name validation; `Enter` assembles the spec.
- Entry: not-a-TTY prints help (`runCmd` with a buffer, no picker).
- End to end against the stub `ssh`: drive the real binary through a pty
  with `script`, send keystrokes, confirm the tunnel opened. This one is
  skipped under `-short`.

And, as always: `make install` and a hash check before it is called done.
