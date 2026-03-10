# Planning Poker App — Technical Specification
**v1.0 · Self-Hosted · Internal Team Tool**

---

## 1. Overview

A self-hosted planning poker app for internal agile estimation sessions. Real-time, no accounts, no seat limits, runs on your existing Apache infrastructure.

### Goals
- Real-time collaborative voting with no page refreshes
- Join by room code — no user accounts or authentication
- Unlimited participants
- Self-hosted on internal Apache servers
- Minimal dependencies, easy to maintain

### Non-Goals
- Persistent vote history across sessions
- Jira / Linear integration
- Native mobile apps (responsive web is sufficient)
- Authentication or access control

---

## 2. Architecture

```
Browser
  ├── GET /            → Apache serves static HTML / CSS / JS
  └── WS  /ws          → Apache proxies to Go server on :8080
```

Apache handles static file serving. The Go binary handles all WebSocket connections and room state in memory. One deployment, one binary.

### Tech Stack

| Layer | Choice | Notes |
|---|---|---|
| Frontend | Vanilla HTML + CSS + JS | No framework, no build step |
| Realtime | Native browser WebSocket API | No client-side libraries needed |
| Backend | Go 1.22+ | Single compiled binary |
| WebSocket library | `github.com/gorilla/websocket` | |
| HTTP router | Chi (`github.com/go-chi/chi`) | Or stdlib `net/http` |
| State | In-memory + `sync.RWMutex` | No database needed |
| Web server | Apache 2.4 | Static files + WS reverse proxy |
| Process manager | systemd | Auto-restart on crash/reboot |

### Repository Layout

```
planning-poker/
├── cmd/
│   └── main.go              # Entry point, HTTP server setup
├── internal/
│   ├── hub/
│   │   ├── hub.go           # Global registry of all active rooms
│   │   ├── room.go          # Room state + broadcast goroutine
│   │   └── client.go        # Per-connection WebSocket handler
│   └── models/
│       └── models.go        # Shared structs: Room, Participant, Message
├── frontend/
│   ├── index.html           # Create / join room screen
│   ├── room.html            # Poker table screen
│   ├── style.css
│   └── app.js               # WebSocket logic + DOM rendering
├── Makefile                 # build, deploy helpers
└── go.mod
```

---

## 3. Backend Specification

### 3.1 HTTP Endpoints

| Endpoint | Description |
|---|---|
| `GET /` | Serve `frontend/index.html` |
| `GET /room/{id}` | Serve `frontend/room.html` (room ID available in URL for JS to read) |
| `GET /static/*` | Serve CSS, JS, and other static assets |
| `GET /ws?room={id}&name={name}` | Upgrade to WebSocket for the given room |

### 3.2 Data Structures

```go
type Room struct {
    ID           string
    Participants map[string]*Client  // keyed by client ID
    Phase        string              // "voting" | "revealed"
    Story        string              // current story / ticket label
    FacilitatorID string             // client ID of the room creator
    History      []RoundResult       // completed rounds this session
    broadcast    chan Message
    register     chan *Client
    unregister   chan *Client
}

type Participant struct {
    ID   string `json:"id"`
    Name string `json:"name"`
    Vote string `json:"vote"` // empty string = not yet voted
}

type RoundResult struct {
    Story     string            `json:"story"`
    Votes     map[string]string `json:"votes"` // name -> card value
    Timestamp time.Time         `json:"timestamp"`
}

type Message struct {
    Type    string          `json:"type"`
    Payload json.RawMessage `json:"payload,omitempty"`
}
```

### 3.3 Concurrency Model

Each Room runs a single goroutine (`room.run()`) that owns all state mutations. Clients communicate with the room via channels only — no shared state races.

- `room.run()` selects on `register`, `unregister`, and `broadcast` channels
- `client.readPump()` runs in its own goroutine, reads WS messages, forwards to room channels
- `client.writePump()` runs in its own goroutine, drains a send channel to the WS connection
- The Hub holds a `map[string]*Room` protected by `sync.RWMutex` for room creation/lookup only

### 3.4 Room Lifecycle

- Room is created on the first WebSocket connection for a new room ID
- The first person to connect becomes the facilitator
- Room is deleted when the last participant disconnects
- If the facilitator disconnects, facilitator role passes to the next connected participant
- Room IDs are 6-character random alphanumeric codes generated client-side and passed in the WS URL query string

---

## 4. WebSocket Message Protocol

All messages are JSON with a `type` string and optional `payload` object.

**The server always responds to any mutation by broadcasting a full `state_update` to every client in the room.** Clients never need to patch state — just replace and re-render.

### 4.1 Client → Server

| type | payload | Description |
|---|---|---|
| `vote` | `{ "card": "5" }` | Cast or change a vote. Must be a value from the active deck. |
| `reveal` | — | Flip all cards. Enforced facilitator-only client-side. |
| `new_round` | `{ "story": "JIRA-123" }` | Archive current votes to history, reset all votes, set new story label. |
| `set_story` | `{ "story": "string" }` | Update the story label without resetting votes. |

### 4.2 Server → Client

| type | payload | Description |
|---|---|---|
| `state_update` | Full room state (see below) | Sent after every state mutation, to all clients. |
| `error` | `{ "message": "string" }` | Sent only to the affected client. |

### 4.3 Full State Payload Shape

```json
{
  "type": "state_update",
  "payload": {
    "roomId": "ABC123",
    "phase": "voting",
    "story": "JIRA-456",
    "facilitatorId": "client-uuid-xyz",
    "participants": [
      { "id": "client-uuid-xyz", "name": "Alice", "vote": "5" },
      { "id": "client-uuid-abc", "name": "Bob",   "vote": ""  }
    ],
    "history": [
      {
        "story": "JIRA-123",
        "votes": { "Alice": "3", "Bob": "5" },
        "timestamp": "2024-01-15T10:30:00Z"
      }
    ]
  }
}
```

During the `voting` phase, each participant's `vote` field should be sent as `"hidden"` to all clients except the voter themselves, to prevent peeking. On `revealed`, send all actual values.

---

## 5. Frontend Specification

### 5.1 Pages

#### `index.html` — Lobby
- Text input: player name
- Button: **Create Room** — generates a random 6-char room ID client-side, redirects to `/room/{id}?name={name}`
- Text input + Button: **Join Room** — enter existing room code, redirects to `/room/{id}?name={name}`

#### `room.html` — Poker Table
Reads `roomId` from URL path and `name` from query string on load, then connects via WebSocket.

**Layout sections:**
1. **Header** — room code (click to copy), story label (editable by facilitator), connection status indicator
2. **Participant grid** — one card-back per participant, name below, checkmark when voted
3. **Card deck** — clickable cards for the local player to vote
4. **Action bar** — Reveal button (facilitator only, active when ≥1 vote cast), New Round button (facilitator only, visible after reveal)
5. **Results panel** — shown after reveal: each participant's card face-up, average score, voting history accordion

### 5.2 Card Decks

Support the following deck. Hard-code as the default; no deck switching required in v1.

```
0, 1, 2, 3, 5, 8, 13, 21, ?, ☕
```

`?` = uncertain, `☕` = need a break. Both are valid vote values; exclude them from average calculation.

### 5.3 WebSocket Client Logic (`app.js`)

```javascript
// Connection
const ws = new WebSocket(`ws://${location.host}/ws?room=${roomId}&name=${name}`);

// Receive
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.type === 'state_update') renderRoom(msg.payload);
  if (msg.type === 'error') showError(msg.payload.message);
};

// Send
const send = (type, payload = {}) =>
  ws.send(JSON.stringify({ type, payload }));

// Re-render entire UI from state snapshot — no incremental patching
function renderRoom(state) { /* rebuild DOM from state */ }
```

Handle disconnection with automatic reconnect (exponential backoff, max 5 attempts). Show a visible "Reconnecting…" banner while disconnected.

### 5.4 Facilitator Controls

Facilitator status is determined by comparing the local client's ID (received in the first `state_update`) against `state.facilitatorId`. Show/hide controls purely in the DOM based on this comparison — no separate auth.