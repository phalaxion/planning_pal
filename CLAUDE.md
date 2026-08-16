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
- **`Client.send` is never closed.** It has several writers, so closing it panics whichever
  one loses the race — and an unrecovered panic in the room goroutine takes down the whole
  process, every room on the server. Shutdown is signalled by closing `Client.done` via the
  `sync.Once`-guarded `Client.shutdown()`. `Client.deliver()` is the only send path and
  `Room.dropClient()` the only eviction path; use them rather than touching the channel.

## Gotchas

- `broadcastStateToAll` rebuilds and re-serializes the payload once per recipient, because
  the vote masking differs per viewer. This is quadratic and **deliberately left alone**:
  measured at 271 µs / 104 KB per broadcast for 20 participants, the stated ceiling
  (`broadcast_bench_test.go`). It mattered when history rode along in every state update;
  it does not now. Revisit only if rooms get much bigger — the fix is one shared masked
  payload plus a small private per-client message carrying that client's own vote, which
  costs a new message type and client-side vote state, so don't pay it speculatively.
- `broadcastStateToAll` snapshots the participant list before delivering, so the broadcast
  that drops an unresponsive client still lists them. The eviction is only visible from the
  next broadcast onward.
- `clientId` lives in `sessionStorage`, so identity survives a refresh but not a tab
  close. Two tabs means two identities sharing one name, which trips `name_taken`.
- The deck contains sentinels. `?` and `☕` are excluded from averages. `999` ("this work
  is too large to quote") is **deliberately included**: one such vote alongside three 5s
  yields an average of 256, and that blow-up is exactly how the room notices someone
  played it. This is intended behaviour — do not "correct" it, and do not backfill the
  `AverageVote` values already stored.
- SQLite's loose type affinity is currently hiding a schema mismatch — see below.

## Work queue

Ordered. Done so far: room state-machine tests, server-side facilitator enforcement, the
`send`-channel lifecycle fix, the `state_update`/`history_update` split, the capped history
window, and the honest CSV export.

1. **`votes.vote` `REAL` → `TEXT` migration.** The column is declared `REAL`, stores text
   (`?`, `☕`), and is scanned back as a string. Fine under SQLite, breaks immediately
   under Postgres or any strict store.
2. **Deck configurable per deployment** via env var or config file. Currently hardcoded in
   `frontend/room/room.js`. Per-room decks are the likely eventual answer, but there's no
   user asking yet.
3. **Create the store directory on startup.** `make run` fails on a fresh clone: it points
   at `./data`, nothing creates it, and `data/` is gitignored. Self-hosters hit this on
   install. `os.MkdirAll` in the store constructors.

Deferred by decision, not oversight: pagination and a full-history view (which would also
let the export cover more than the window), per-room decks, room passphrases and invite
tokens, SSO.

## Known-open, deliberately unscheduled

- `CheckOrigin` returns `true` for every origin (`cmd/main.go`).
- `store.Save` runs synchronously inside `room.run()`, so slow disk I/O stalls the room.
- `SQLiteStore.Get` ignores its `roundId` when selecting the round, then fetches a
  different round's votes. Appears to be dead code.

## Conventions

- `make fmt` before staging. `make run` for local dev (SQLite, `./data`).
- Frontend is dependency-free vanilla JS. No build step, no framework — keep it that way.
- Never run `git commit`. Stage with `git add` and stop.
