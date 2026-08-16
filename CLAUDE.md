# Planning Pal

Self-hosted planning poker. Go single binary + vanilla JS, in-memory rooms, pluggable
persistence (JSON or SQLite).

This file holds the decisions and invariants that **cannot be recovered by reading the
code**. The codebase is ~2,000 lines and small enough to read — this is not a spec, and
it should not restate what `room.go` already says.

## Product direction

- **Self-hosted, not SaaS.** A company deploys its own copy behind its own proxy. There
  is no hosted offering and no plan for one.
- **Paid surface is tracker integrations + cross-sprint analytics.** Pulling a backlog in
  from Jira/Linear/Azure DevOps and pushing estimates back is the product. The voting UI
  is table stakes and stays free.
- **Scale is small rooms, one server, forever.** Realistic ceiling is ~20 people per room
  and a handful of concurrent rooms. Sharding, Redis/NATS pub-sub, and multi-instance
  room ownership are explicitly **out of scope** — do not propose them.

## Trust model

Open inside the perimeter, **deliberately**. Knowing a room code grants entry and access
to that room's full history. This is acceptable because the intended deployment puts an
SSO layer in front of the whole app, so anyone who can reach a room code is already an
employee.

- Room passphrases and invite tokens are a someday, not a now.
- **Seam to protect:** the server currently trusts the self-asserted `name` query param
  (`cmd/main.go`). Adding SSO means preferring a trusted proxy header
  (`X-Forwarded-User` or similar) over that param. Don't build it yet; don't foreclose it.
- History persisting across room re-entry is **intentional**. A team reusing a room code
  every sprint expects its past rounds to still be there.

## Architecture invariants

- **`Room.run()` is the only goroutine permitted to mutate room state** (participants,
  phase, story, history, facilitator). Everything else communicates via the `register`,
  `unregister`, and `inbound` channels. Do not add locks to `Room`; do not touch its
  fields from a handler or timer callback — signal the run loop instead.
- `GlobalHub`'s room map is the one place a mutex is correct.
- Timer callbacks use the non-blocking `signal()` helper so a wedged run loop can't leak
  goroutines.

## Gotchas

- `broadcastStateToAll` rebuilds and re-serializes the **entire** payload once per
  recipient. Vote masking is why. It should become one shared masked payload plus a small
  private per-client message carrying that client's own vote.
- A full `send` buffer **closes the client's channel** rather than dropping the message.
  Oversized broadcasts therefore present as random disconnects mid-vote, not as slowness.
- `clientId` lives in `sessionStorage`, so identity survives a refresh but not a tab
  close. Two tabs means two identities sharing one name, which trips `name_taken`.
- The deck contains sentinels that are not estimates: `?`, `☕`, and `999`
  ("too large to quote"). Any averaging must exclude all three.
- SQLite's loose type affinity is currently hiding a schema mismatch — see below.

## Work queue

Ordered. Tests come first because items 2–4 all change what every client receives, and
there is currently no way to detect a regression in the reconnect or facilitator-handoff
paths except by reproducing it by hand.

1. **Room state-machine tests.** Add a seam for `Client.conn` (interface or nil guard —
   `run()` calls `conn.Close()` on the reconnect path). Cover facilitator handoff and
   grace-period restore, reconnect-by-`clientId`, stale unregister, name collision.
2. **Server-side facilitator enforcement** on `reveal`, `new_round`, `set_story`, and
   `promote`. Today these are unchecked; the frontend only hides the buttons.
3. **Split the protocol.** `state_update` carries live state only. A new `history_update`
   is sent on join and on `new_round`. History changes only on `new_round`, so shipping
   it on every vote is pure waste.
4. **Cap the history window to 10.** `r.history` becomes a rolling 10-item window; `List`
   gains `ORDER BY timestamp DESC LIMIT 10`. It currently has no `ORDER BY` at all and
   works only because SQLite returns rowid order.
5. **Demote the CSV export.** It builds from `state.history`, so a capped window silently
   turns it into "export last 10" while still looking complete. Relabel or remove.
6. **Exclude `999` from averages** in both the results summary and the `new_round`
   payload. Persisted `AverageVote` values are wrong wherever a `999` was cast.
7. **Single-serialization broadcast** (see Gotchas).
8. **`votes.vote` `REAL` → `TEXT` migration.** The column is declared `REAL`, stores text
   (`?`, `☕`), and is scanned back as a string. Fine under SQLite, breaks immediately
   under Postgres or any strict store.
9. **Deck configurable per deployment** via env var or config file. Currently hardcoded
   in `frontend/room/room.js`. Per-room decks are the likely eventual answer, but there's
   no user asking yet.

## Known-open, deliberately unscheduled

- `CheckOrigin` returns `true` for every origin (`cmd/main.go`).
- `store.Save` runs synchronously inside `room.run()`, so slow disk I/O stalls the room.
- `SQLiteStore.Get` ignores its `roundId` when selecting the round, then fetches a
  different round's votes. Appears to be dead code.

## Conventions

- `make fmt` before staging. `make run` for local dev (SQLite, `./data`).
- Frontend is dependency-free vanilla JS. No build step, no framework — keep it that way.
- Never run `git commit`. Stage with `git add` and stop.
