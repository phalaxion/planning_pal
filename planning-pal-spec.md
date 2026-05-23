# Planning Pal App — Technical Specification
**current implementation — self-hosted, single-binary server with optional persistent history**

---

## 1. High level summary

This repository implements a small planning-poker application with a Go backend that manages rooms and WebSocket connections, and a Vanilla JS frontend that renders the UI and speaks a simple JSON WebSocket protocol.

Key behaviours of the current codebase (as implemented):
- Rooms and active participants are kept in-memory; a pluggable persistent store is available for completed round history (JSON files or SQLite).
- No user accounts are required. Clients may provide a clientId on connect to persist identity across reconnects; the server will generate a UUID when one is not supplied.
- Votes are hidden from other participants during the "voting" phase and revealed when the facilitator triggers a reveal.
- Facilitator role is assigned to the first connected client; if they disconnect a short grace period is given (5s) for them to reconnect and retain the role; otherwise the role is reassigned to another participant.
- When the last participant leaves, the room is kept for a short time (5s) and then cleaned up to free memory.

---

## 2. Architecture & process

Browser
  ├── GET /            → server serves static HTML/CSS/JS from the `frontend/` directory (configurable via STATIC_PATH)
  └── WS  /ws          → WebSocket endpoint handled by the Go binary

The binary (cmd/main.go) uses the stdlib HTTP multiplexer and gorilla/websocket for upgrades. Static assets are served under `/static/` (served from the STATIC_PATH directory). The lobby page is `frontend/lobby/lobby.html` and the room page is `frontend/room/room.html`.

Environment variables of note:
- STATIC_PATH — directory containing frontend files (defaults to `frontend` for local dev)
- PPAL_STORE_PATH — path used by the persistent store (defaults to `/var/lib/planning-pal`)
- PPAL_STORE_TYPE — either `json` or `sqlite` (defaults to `json`)

---

## 3. Backend implementation details

Files of interest:
- cmd/main.go — HTTP handlers and WebSocket upgrader
- internal/hub/hub.go — GlobalHub, room registry, store initialization
- internal/hub/room.go — Room state machine, message handling, timers, broadcast logic
- internal/hub/client.go — per-connection read/write pumps and client lifecycle
- internal/models/models.go — shared model types
- internal/store/* — JSON and SQLite store implementations for RoundResult history

Store configuration and behavior:
- The Hub initializes a store based on PPAL_STORE_TYPE (`json` or `sqlite`).
- The Store interface exposes Get, List, Save, Delete for RoundResult entries keyed by room ID.
- On room creation the room attempts to List existing history from the configured store and attaches it to the in-memory room.history slice.

Concurrency model:
- Each Room has a single goroutine (Room.run()) that owns mutations to the room state (participants, phase, story, history).
- Client connections spawn two goroutines: readPump (reads WS and forwards parsed messages to room.inbound) and writePump (sends messages from a buffered channel to the socket).
- The GlobalHub map of rooms is guarded by a sync.RWMutex for safe lookup/creation/deletion.

Room lifecycle details implemented:
- GetOrCreateRoom(roomID) creates the Room object and starts its run loop when the first client connects.
- On client register: rejects duplicate display names (sends a server error of type `name_taken`), treats a second connection with the same clientId as a reconnect by closing the existing connection, and assigns facilitator if none present. If the reconnect happens while a facilitator grace timer is active the reconnecting client regains the facilitator role.
- On client unregister: if the leaving client was the facilitator a 5 second timer is started; if the facilitator doesn't return before the timer fires, the facilitator slot is cleared and then reassigned to another connected participant. When the last participant leaves a 5 second cleanup timer runs and removes the room from GlobalHub.

Message handling implemented by the Room (room.handleClientMessage):
- `vote` — payload { card }
  - Updates the Participant.Vote for that client (participant.Voted is set on broadcast based on non-empty vote)
  - Broadcasts new full state
- `reveal` — sets room.phase = `revealed` and broadcasts state
- `new_round` — payload { lastRoundAverage, story }
  - Collects current votes into a RoundResult (includes generated ID via guid library and Timestamp UTC)
  - Saves the result via the configured Store (Save)
  - Appends result to room.history
  - Resets participant votes to empty, sets room.story to provided story and phase to `voting`, broadcasts state
- `set_story` — payload { story }
  - Updates room.story and broadcasts

Broadcasting behaviour:
- broadcastStateToAll builds a tailored payload per recipient. It includes:
  - roomId, phase, story, facilitatorId, participants (array), history, youId
- During `voting` phase, other participants' non-empty votes are replaced with the literal string `"hidden"