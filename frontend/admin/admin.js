import Connection from "../core/Connection.js";

(function () {
	const clientId = getClientId();
	const name = getName();
	const roomId = getRoom();

	if (!name) {
		const errorMessage = "A name must be provided to join a room"
		location.href = `/?error=missing_name&message=${encodeURIComponent(errorMessage)}`
		return;
	}

	const ws = new Connection(clientId, name, roomId);
	ws.connect(
		(ev) => {
			const msg = JSON.parse(ev.data)
			const payload = (typeof msg.payload === 'string') ? JSON.parse(msg.payload) : msg.payload

			if (msg.type === 'error' && payload.fatal === "Yes") {
				location.href = `/?error=${payload.code}&message=${encodeURIComponent(payload.message)}`
			}
			else if (msg.type === 'error') {
				alert(`Error: ${payload.message}`)
			}
			else if (msg.type === 'state_update') {
				renderRoom(payload)
			}
			else {
				console.log('Unknown Message', msg);
			}
		}, 
		(type, message) => {
			console.log(type, message);
			
			const el = qs('#status');
			if (!el) return;

			el.innerHTML = `<b>${type}:</b><br>${message}`;
			el.style.display = message ? 'inline-block' : 'none';
		}
	);

	function renderRoom(state) {
		const youId = state.youId

		// ── Story ──────────────────────────────────────────────────
		const storyEl = qs('#story')
		if (storyEl.textContent != state.story) {
			storyEl.textContent = state.story || 'No story set';
		}

		// ── Participants ───────────────────────────────────────────
		const p = qs('#participants')
		p.innerHTML = ''

		const participants = (Array.isArray(state.participants) ? state.participants.slice() : []);
		participants.sort((a, b) => {
			if (b.id == state.youId) return 1 // the user is always first
			if (a.id == state.youId) return -1 // the user is always first
			return (a.name || '').localeCompare(b.name || '') // otherwise sort by name
		})

		participants.forEach(pt => {
			const isYou = pt.id === youId
			const voted = !!pt.voted

			const card = document.createElement('div')
			card.className = 'p-card' + (isYou ? ' is-you' : '')

			if (pt.id === state.facilitatorId) {
				card.className += ' p-facilitator';
			}

			const nameEl = document.createElement('div')
			nameEl.className = 'p-name' + (isYou ? ' is-you' : '')
			nameEl.textContent = pt.name
			card.appendChild(nameEl)

			p.appendChild(card)
		})

		// ── Debug ──────────────────────────────────────────────────
		const dbg = qs('#debug')
		if (dbg && getDebug()) {
			dbg.style.display = 'block';
			dbg.textContent = `youId: ${youId}\nfacilitatorId: ${state.facilitatorId || ''}\nphase: ${state.phase || ''}\nstory: ${state.story || ''}`
		}
	}
})();
