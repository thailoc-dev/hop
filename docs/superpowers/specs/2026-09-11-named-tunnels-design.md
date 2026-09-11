# Named tunnels — design

Status: draft — awaiting review
Date: 2026-09-11
Builds on: `2026-09-10-hop-design.md`

## Why

The tunnel you open most is the one you opened yesterday. Today that costs four
long tokens every time:

```bash
hop example-tracker-dev tracker_redis_staging 6379 46379
```

Completion helps, but a cold host still needs two Tab presses, and none of it
helps you *remember* which local port you used last time. A name does:

```bash
hop redis-stg
```

## Goals

- Opening a tunnel you have opened before costs one word.
- The bare four-argument form is untouched; nothing existing changes meaning.
- Saved tunnels are visible without a daemon running.
- Every command that takes a local port also takes a name.

## Non-goals

- Groups or profiles (`hop up all-staging`). One name, one tunnel.
- Syncing the catalogue between machines.
- Auto-remembering unnamed tunnels. A tunnel is saved only when named.
- Any change to the state machine, supervisor lifecycle, or daemon exit rule.

## Command surface

```
hop <name>                                    open a saved tunnel
hop up <name>                                 same, explicit form
hop <host> <container> <remote> <local> --name <name>
                                              open and save in one step
hop save <name> [local-port]                  save a running tunnel under a name
hop forget <name>                             remove a name from the catalogue
hop down | logs | restart <name-or-port>      names accepted wherever ports are
```

### Disambiguating the bare form

The existing rule reads a reserved first word as a subcommand, and otherwise
requires exactly four arguments. It gains one clause:

- **One argument** that is not a reserved word is a saved name.
- **Four arguments** are a tunnel spec, as today.
- Any other count is a usage error naming all three forms.

A single argument is a usage error today, so the slot is free and no existing
invocation changes meaning. `up`, `save` and `forget` join the reserved words.

### Names

A name matches `^[a-z0-9][a-z0-9_-]{0,39}$`, is not a reserved word, and
**contains at least one non-digit**. The last rule exists because `hop down`
and its siblings accept both names and ports: an all-digit argument is a port,
anything else is a name, and the two sets must not overlap.

Saving a name that already exists overwrites it. That is how a saved tunnel's
ports are changed: open it the long way with the new values and `save` again.

### `hop save`

`hop save <name> [local-port]` copies a running tunnel's spec into the
catalogue. With no port it takes the **most recently opened** tunnel. "Most
recently opened" is by creation time, not by the `Since` field: `Since` resets
on every state transition, so a tunnel that reconnected a second ago would
otherwise look newer than one opened a second after it.

With no running tunnels, `save` is a usage error explaining what it needs.
With a port that has no running tunnel, it fails with the existing
`no tunnel on local port N` message.

`--name` on the four-argument form is `open` followed by `save`, in that order:
the name is validated before anything is opened, so an invalid name fails
without side effects, and an existing name is overwritten exactly as `save`
would.

### `hop forget`

Removes the name from the catalogue. A running instance of that tunnel keeps
running and keeps its name in `ls` until it stops; forgetting is about the
future, not the present.

### Opening a saved tunnel

`hop <name>` loads the spec and sends the daemon the same `add` request the
four-argument form sends, with the name attached. Everything downstream —
resolution, port checks, supervision — is unchanged, and so are the flags:
`--wait`, `--attach` and `--env` mean what they mean on the four-argument form,
with `--env` overriding the saved label for this run only. Three cases are
decided:

- **Local port busy** → the existing error naming the holder. No silent
  fallback to another port: the saved port is the one the database client is
  configured for.
- **Already running** → `redis-stg is already running on 46379`, exit 0. Asking
  for something you already have is not an error.
- **Unknown name** → `no saved tunnel named "x"`, and if `x` is also not an ssh
  host alias, that is all; if it *is* a host alias, the message adds that a host
  needs the four-argument form.

## Output

`hop ls` gains a `NAME` column, blank for unnamed tunnels, and lists saved
tunnels that are not running as dimmed rows in state `saved`. One command
answers "what do I have", running or not.

```
$ hop ls
NAME       LOCAL  ENV   HOST                  CONTAINER               REMOTE  STATE    SINCE  RETRIES
redis-stg  46379  stg   example-tracker-dev   tracker_redis_staging   6379    healthy  2h14m  0
           27018  stg   example-backend-dev   app_mongo_staging       27017   healthy  18m    0
mongo-dev  27019  dev   example-backend-dev   app_mongo_dev           27017   saved    —      —
```

`--json` includes saved rows with `"state": "saved"` and no runtime fields.

The open line and every other place a tunnel is printed show the name when
there is one:

```
$ hop redis-stg
  stg redis-stg (tracker_redis_staging)  →  localhost:46379   healthy
```

## Persistence

### Catalogue file

`~/.hop/tunnels.json`, owned by the CLI. The daemon never reads it.

```json
{
  "version": 1,
  "tunnels": {
    "redis-stg": {
      "host": "example-tracker-dev",
      "container": "tracker_redis_staging",
      "remote_port": 6379,
      "local_port": 46379,
      "env": "stg",
      "autostart": false
    }
  }
}
```

Written atomically through the same temp-file-and-rename path as
`state.json`; the `store` package is generalised to take a target path and a
value rather than being specific to the running-tunnel list. A corrupt
catalogue is moved aside as `tunnels.json.corrupt` and treated as empty, as
`state.json` already is.

### Running state

`tunnel.Spec` gains `Name string` (`json:"name,omitempty"`). It travels in the
existing `add` request and persists in `state.json` like every other spec
field, so a daemon restart keeps names. `tunnel.Status` gains
`CreatedAt time.Time`, set once in `tunnel.New`.

No new control operation is needed. `save` is implemented client-side from a
`list` response; `forget` touches only the catalogue.

## Completion

- Position 0 of the bare form offers saved names alongside hosts and
  subcommands, each described as `env container@host`.
- `down`, `logs`, `restart` offer running tunnels by name where one exists,
  falling back to the port, with the same description.
- `forget` offers saved names.
- `save`'s first argument offers nothing (it is a new name); its optional second
  offers running ports.

## Architecture

| Change | Where |
|---|---|
| `Name` on `Spec`; `CreatedAt` on `Status` | `internal/tunnel` |
| Generalised atomic read/write; catalogue type | `internal/store` |
| `tunnels.json` path | `internal/hopfs` |
| Name validation, one-argument dispatch, `up`/`save`/`forget`, name resolution for port-addressed commands, `ls` rendering | `cmd/hop` |
| Completion sources | `cmd/hop/completion.go` |

The process-spawning rule, the state machine, the supervisor's watch loop and
the control protocol's operation set are unchanged.

## Testing

Hermetic, as before:

- Catalogue: round trip, atomic write, corrupt-file recovery, overwrite on
  re-save, `forget` of an unknown name.
- Name validation: table over valid names, each rejection rule, and every
  reserved word.
- Argument dispatch: one argument → name, four → spec, two/three/five → usage
  error naming all forms.
- `save`: picks the newest `CreatedAt` when no port is given; explicit port;
  no running tunnels.
- Name resolution: `down`, `logs`, `restart` each accept a name and a port.
- `ls`: NAME column, saved rows dimmed, `--json` shape.
- Completion: names at position 0; names for the port-addressed commands.
- End-to-end, against the stub ssh: open with `--name`, `ls` shows the name,
  `down`, `hop <name>` reopens on the same port, `forget`, `hop <name>` now
  fails with the unknown-name message.

And, because this week showed why: the plan's final task runs `make install`
and checks `shasum ~/.local/bin/hop bin/hop` before claiming anything works.

## Migration

None. A `state.json` without `name` fields loads as unnamed tunnels; a missing
`tunnels.json` is an empty catalogue. Nothing a user has today changes meaning.
