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

- **Stale frontends are guarded twice, and both halves matter.** The frontend and the
  websocket protocol it speaks deploy together but cache separately, so a browser running
  an old `room.js` against a new server fails *silently* — it renders the wrong thing
  rather than erroring.
  1. Everything is served `Cache-Control: no-cache` — revalidate, not don't-store, so an
     unchanged file is an empty 304. **This is the load-bearing one.**
  2. HTML pages carry `?v=` on every asset URL, from the `assetVersion` constant in
     `cmd/main.go`, substituted into the `__ASSET_VERSION__` placeholder at serve time.
     Bump it by hand to force a refetch. This only matters if something between the server
     and the browser ignores the header, so a forgotten bump is harmless — which is why it
     is one constant and not a content hash.
  The `?v=` covers assets referenced from HTML. It does **not** reach ES module imports —
  `room.js` imports `../core/Connection.js` unversioned, since import URLs resolve relative
  to the importing module. `no-cache` is what covers those, which is why both halves stay.

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
- The deck lives on the server (`PPAL_DECK`, default in `hub.defaultDeck`) and is sent to
  each client on join. The frontend holds no copy — don't reintroduce one. Averaging drops
  anything non-numeric, so it works for any deck without knowing the faces.
- `999` ("this work is too large to quote") is a sentinel that is **deliberately averaged
  as a number**: one such vote alongside three 5s yields 256, and that blow-up is exactly
  how the room notices someone played it. This is intended — do not "correct" it.
- **A round's average is computed server-side** (`averageVote` in `room.go`) from the real
  votes. It cannot be done in the browser: during the voting phase every other participant's
  vote reads `"hidden"`, so a client closing a round without revealing first would record
  only its own vote as the average. `AverageVote` values stored before this was fixed are
  wrong for any round closed without a reveal, and are not backfilled.
- A stored `AverageVote` of 0 means no numeric votes were cast, not an average of zero —
  the UI shows it as `—`. A deck containing a literal `0` face would blur that distinction.
- SQLite gave `votes.vote` REAL affinity when it was declared REAL, so numeric card faces
  are stored as `5.0`, not `"5"`. They read back as `"5"` only because Go formats the float
  that way. Any future move of that column — or a port to another engine — has to convert
  via INTEGER, not straight to TEXT, or every numeric vote becomes `"5.0"`.

## Work queue

**The queue is empty.** Completed: room state-machine tests, server-side facilitator
enforcement, the `send`-channel lifecycle fix, the `state_update`/`history_update` split,
the capped history window, the honest CSV export, the `votes.vote` REAL→TEXT migration,
`Cache-Control: no-cache` on static assets, the configurable deck, and store directory
creation on startup.

Deferred by decision, not oversight:

- Pagination and a full-history view — would also let the export cover more than the window
- Per-room decks — `PPAL_DECK` is currently one deck for the whole server
- Room passphrases and invite tokens
- SSO, and with it reading identity from a trusted proxy header rather than the `name` query param
- Tracker integrations and cross-sprint analytics, i.e. the actual paid surface

## Known-open, deliberately unscheduled

- `CheckOrigin` returns `true` for every origin (`cmd/main.go`).
- `store.Save` runs synchronously inside `room.run()`, so slow disk I/O stalls the room.
- `SQLiteStore.Get` ignores its `roundId` when selecting the round, then fetches a
  different round's votes. Appears to be dead code.
- All three HTML pages pull webfonts from `fonts.googleapis.com`. For a self-hosted product
  that is an outbound dependency a customer's network may block, and it leaks that they run
  this app. Self-hosting the fonts would remove the only external request the app makes.

## Conventions

- `make fmt` before staging. `make run` for local dev (SQLite, `./data`).
- Frontend is dependency-free vanilla JS. No build step, no framework — keep it that way.
- SQLite schema changes are migrations in `applyMigrations`, gated on `PRAGMA user_version`
  (currently 2). Add a new migration; never edit an existing one — deployed databases have
  already run it.
- Never run `git commit`. Stage with `git add` and stop.
