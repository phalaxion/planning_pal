export default class Connection {
	#ws = null;
	#attempts = 0;
	#queue = [];

	constructor(clientId, name, roomId) {
		this.clientId = clientId;
		this.name = name;
		this.roomId = roomId
	}

	connect(onMessage = null, attemptHandler = (type, message) => {}) {
		const url = `${location.protocol.replace('http', 'ws')}//${location.host}/ws?room=${this.roomId}&name=${encodeURIComponent(this.name)}&clientId=${encodeURIComponent(this.clientId)}`
		const ws = new WebSocket(url);

		ws.onopen = () => {
			attemptHandler('Connected', '');
			this.#attempts = 0
			while (this.#queue.length) {
				ws.send(this.#queue.shift())
			}
		}

		ws.onclose = (() => {
			const maxAttempts = 5;

			if (this.#attempts >= maxAttempts) {
				attemptHandler('Failed', 'Reconnection Failed');
				return;
			}
		
			this.#attempts++;
			const backoff = Math.min(1000 * Math.pow(1.5, this.#attempts - 1), 16000);
			
			setTimeout(() => {
				this.connect(onMessage, attemptHandler);
			}, backoff)

			attemptHandler('Reconnecting', `Attempt ${this.#attempts} / ${maxAttempts}`);
		});

		ws.onmessage = onMessage || ((ev) => {});

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