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
- **`promote` is deliberately open to anyone in the room**, unlike `reveal`, `new_round`
  and `set_story`, which are facilitator-only. The role goes to whoever connects first,
  which is rarely the person who should run the session — so the admin page is how a scrum
  master who joined second takes control. Gating it behind the role it grants would make it
  useless in exactly the case it exists for. It is not an oversight; do not "fix" it.
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
- The `Hub`'s room map is the one place a mutex is correct.
- **The store is injected, not global.** `main()` builds it via `hub.StoreFromEnv()` and
  hands it to `hub.NewHub`; a `Room` holds a `*Hub` so it can remove itself when empty.
  This used to be `var GlobalHub = NewHub()` — importing the package created directories
  and could `log.Fatalf`, so a test binary died before running anything. Don't reintroduce
  package-level state that touches the filesystem at init.
- **SQLite is the only store.** The JSON backend was retired because every stored feature
  had to be written twice. `PPAL_STORE_TYPE=json` now fails loudly at startup.
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
  1. **HTML pages** are `Cache-Control: no-cache` — revalidate, not don't-store, so an
     unchanged page is an empty 304. A page cannot carry a version in its own URL, so this
     is what gets a browser the current asset URLs. Do not remove it.
  2. **Assets** are `public, max-age=31536000, immutable`, because their URLs *are*
     versioned: HTML carries `?v=` from the `assetVersion` constant in `cmd/main.go`,
     substituted into the `__ASSET_VERSION__` placeholder at serve time.
  **Bump `assetVersion` whenever anything under `frontend/` changes.** It is no longer
  belt and braces — it is the only thing that gets a changed file to a browser, and a
  forgotten bump strands clients on old JS for a year with no self-heal.
  `TestAssetVersionMatchesTheFrontend` guards this: `assetFingerprint` records the frontend
  the current version describes, and the test fails with the value to paste in when they
  drift. Never edit the fingerprint without bumping the version.
  **Every script is referenced from the HTML** so that all of them carry `?v=`. `?v=` does
  not reach ES module imports — an import URL resolves relative to the importing module and
  cannot carry the version — which once left `Connection.js` stale while `room.js` was
  fresh, so a caller was running against a dependency that lacked the method it called.
  `Connection.js` is therefore a plain global script like `core.js`, not a module. Don't
  reintroduce `import` between frontend files without solving versioning first.

- **Room codes are canonicalised to uppercase.** `/room/abc123` redirects to `/room/ABC123`
  and the `/ws` handler uppercases too, since that is the key rooms and stored history use
  and tools connect to the socket without loading a page. Without this a mistyped or
  chat-lowercased URL opened a second, empty room that looked exactly right.
- **Invite links point at the lobby** (`/?room=CODE`), not at the room. A room URL carries
  `?name=`, so a copied one made the recipient join as the sender — `name_taken`, which is
  fatal, and they lost the code. The lobby prefills the room so they enter their own name.

- **The queue is durable per-room state, unlike everything else about a room.** Items are
  captured during the day for a session the next morning, so they live in SQLite and
  survive the room being torn down. Only `pending` items load into a room; `done` ones stay
  as a record and never come back, so a session opens on a clean list.
- An item is retired **only when a round is actually recorded for it** — start something,
  discuss it, move on without voting and it stays pending. `Room.activeItemID` tracks which
  item backs the current story; a hand-typed story leaves it empty and retires nothing.
- The active item is filtered out of `queue_update`: it isn't *next*, it's *now*. That is
  also why an abandoned item reappears — nothing changed its status, it just stopped being
  active.
- **Notes are user text rendered to the whole room.** `appendLinkified` builds `<a>` nodes
  and never assembles HTML; using `innerHTML` there would be stored XSS delivered to
  everyone who opens the room. Only `http(s)` is matched, so no `javascript:` href.

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
- **A round with no votes is not recorded.** Skipping past a story is normal, and saving
  the empty round would burn a slot in the ten-round window and leave a permanent blank row.
  The round still advances; only persistence is skipped.
- Display names are unique **case-insensitively** (`strings.EqualFold`). Stored votes are
  keyed by name, so allowing `Alice` and `alice` would split one person into two history
  columns while looking identical in the participant list.
- During the facilitator's grace period the room genuinely has no facilitator, so every
  client hides the controls. `awaitingFacilitator` carries the departed facilitator's name
  through that window purely so the UI can explain itself; it changes nothing about
  reassignment. `lastFacilitatorName` exists because by then they are out of `participants`.
- **Votes can still be changed after a reveal, on purpose.** Revealing starts a discussion
  phase where people are meant to move their estimate as the conversation changes their
  mind, so `vote` has no phase guard. This reads like a missing check — it isn't.
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
