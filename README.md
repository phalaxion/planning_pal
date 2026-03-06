Planning Poker — Minimal self-hosted server

Quick start (local development)

Prereqs: Go 1.22+

Build:

```bash
make build
```

Run:

```bash
make run
# or
go run ./cmd
```

Open: http://localhost:8080/

WebSocket endpoint: `ws://localhost:8080/ws?room=ROOMID&name=NAME`

Useful tools:

- `tools/wsmon` — small CLI monitor to print incoming messages from the server:

```bash
go run ./tools/wsmon -room=TEST -name=Alice
```

Testing tips:

- Open two browser tabs and create/join the same room code. The first connector
  becomes the facilitator and should see enabled Reveal/New Round buttons.
- Use the `debug` box on the room page to confirm `youId` and `facilitatorId`
  values.

Next steps (planned): polishing UI, adding tests, packaging for systemd/docker.
