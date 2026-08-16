// Loaded as a plain script from the HTML, not imported as a module, so that it
// picks up the ?v= cache key like every other asset. An ES import resolves
// relative to the importing module and cannot carry the version, which left
// this file able to go stale while room.js was fresh.
class Connection {
	#ws = null;
	#attempts = 0;
	#queue = [];
	#onMessage = null;
	#attemptHandler = () => { };

	constructor(clientId, name, roomId) {
		this.clientId = clientId;
		this.name = name;
		this.roomId = roomId
	}

	connect(onMessage = null, attemptHandler = (type, message) => { }) {
		this.#onMessage = onMessage;
		this.#attemptHandler = attemptHandler;
		this.#open();
	}

	// retry resets the backoff and tries again. Without it, a client that has
	// exhausted its attempts has no way back except reloading the page — and
	// nothing on screen says so.
	retry() {
		this.#attempts = 0;
		this.#open();
	}

	#open() {
		const url = `${location.protocol.replace('http', 'ws')}//${location.host}/ws?room=${this.roomId}&name=${encodeURIComponent(this.name)}&clientId=${encodeURIComponent(this.clientId)}`
		const ws = new WebSocket(url);

		ws.onopen = () => {
			this.#attemptHandler('Connected', '');
			this.#attempts = 0
			while (this.#queue.length) {
				ws.send(this.#queue.shift())
			}
		}

		ws.onclose = (() => {
			const maxAttempts = 5;

			if (this.#attempts >= maxAttempts) {
				this.#attemptHandler('Disconnected', 'Could not reach the server.');
				return;
			}

			this.#attempts++;
			const backoff = Math.min(1000 * Math.pow(1.5, this.#attempts - 1), 16000);

			setTimeout(() => {
				this.#open();
			}, backoff)

			this.#attemptHandler('Reconnecting', `Attempt ${this.#attempts} / ${maxAttempts}`);
		});

		ws.onmessage = this.#onMessage || ((ev) => { });

		ws.onerror = (error) => {
			console.error(`Something went wrong: ${error}`)
			ws.close();
		};

		this.#ws = ws;
	}

	send(type, payload) {
		const m = JSON.stringify({ type, payload });

		// If we don't have a websocket yet or it's become inactive add it to a queue to send
		// this queue will be sent in order as part of the ws opening process.
		if (!this.#ws || this.#ws.readyState !== WebSocket.OPEN) {
			this.#queue.push(m);
			return;
		}

		this.#ws.send(m);
	}
}
