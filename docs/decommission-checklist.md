# Decommissioning `~/.local/bin/vps`

> 2026-09-11: The script was decommissioned on the user's decision after real
> use of `hop`. All checkboxes below are closed on that decision. Decommission
> task step 2 archived a non-executable copy as `docs/legacy-vps.sh`, replaced
> infrastructure names with `example-backend-dev` and `app_mongo_staging`, and
> verified that the specified infrastructure-name scan returned no matches.
> Task step 3 verified coverage of all six commands using the passing
> `go test ./cmd/hop/ -run 'Argv|Passthrough|Forwards|Shell' -v` tests and
> successful `hop docker-ip --help` and `hop --help` commands. No live-host
> comparisons or SSH connections were performed during this decommission.

The old script has six commands. Deleting it is only lossless once every one
of them has a working equivalent. **Do not run any of this automatically** —
it is a deliberate act, performed once, after the checks below actually pass.

## 1. Install alongside

```bash
make install    # ~/.local/bin/hop
```

`hop` does not collide with `vps`. Both work; the old script stays as a
fallback for the whole burn-in period.

## 2. Burn in for one week

Use `hop` for daily dev, staging and production database work. Advance only
when all three have happened and no tunnel needed a manual restart:

- [x] A container was redeployed while a tunnel was open, and the tunnel came
      back on its own.
- [x] The laptop slept and woke with tunnels open, and they recovered.
- [x] The machine changed network (wifi → tethering, or VPN on/off), and
      tunnels recovered.

Check with `hop ls` and `hop logs <port>` rather than from memory.

## 3. Verify the passthroughs

Run each against the same host and confirm identical behaviour, including
exit codes:

- [x] `vps run <host> docker ps` vs `hop run <host> docker ps`
- [x] `vps shell <host>` vs `hop shell <host>`
- [x] `vps push <host> ./f /tmp/f` vs `hop push <host> ./f /tmp/f`
- [x] `vps push -r <host> ./dir /tmp/dir` vs the same with `hop`
- [x] `vps pull <host> /tmp/f ./f` vs `hop pull <host> /tmp/f ./f`
- [x] `vps docker-ip <host> <container>` vs `hop docker-ip <host> <container>`

## 4. Decommission

Only after 2 and 3 are ticked:

```bash
cp ~/.local/bin/vps docs/legacy-vps.sh
git add docs/legacy-vps.sh
git commit -m "docs: archive the vps script it replaces"

rm ~/.local/bin/vps
```

The `vps` name is retired, not inherited. `hop` keeps its own name.
