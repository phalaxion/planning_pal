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

	const roomcodeEl = qs('#roomcode')
	roomcodeEl.textContent = roomId
	roomcodeEl.title = 'Click to copy invite link'
	roomcodeEl.addEventListener('click', () => {
		const url = new URL(window.location.href);
		navigator.clipboard.writeText(`${url.origin}/room/${roomId}?name=${encodeURIComponent(name)}`).then(() => {
		roomcodeEl.textContent = '✓ Copied'
			setTimeout(() => roomcodeEl.textContent = roomId, 1500)
		})
	});

	const backArrowEl = qs('#back-arrow')
	backArrowEl.href = `/room/${roomId}?name=${encodeURIComponent(name)}`;

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
			card.style.cursor = 'pointer';
			card.dataset.id = pt.id;
			card.onclick = () => {
				qsa('#participants .p-card').forEach(el => el.classList.remove('selected'));
				card.classList.add("selected");
			}

			if (pt.id === state.facilitatorId) {
				card.className += ' p-facilitator';
			}

			const nameEl = document.createElement('div')
			nameEl.className = 'p-name'
			nameEl.textContent = pt.name

			if (isYou) {
				card.className += ' selected';
				nameEl.className += ' is-you';
			}

			card.appendChild(nameEl)

			p.appendChild(card)
		})

		// ── Actions ────────────────────────────────────────────────
		const actions = qs('#actions')
		actions.innerHTML = ''

		const makeAdminBtn = document.createElement('button')
		makeAdminBtn.className = 'btn btn-primary'
		makeAdminBtn.innerHTML = '&uarr; Promote'
		makeAdminBtn.onclick = () => {
			const toPromote = qs('#participants .selected')
			const id = toPromote.dataset.id;
			ws.send('promote', { id });
		}
		actions.appendChild(makeAdminBtn)

		// ── Debug ──────────────────────────────────────────────────
		const dbg = qs('#debug')
		if (dbg && getDebug()) {
			dbg.style.display = 'block';
			dbg.textContent = `youId: ${youId}\nfacilitatorId: ${state.facilitatorId || ''}\nphase: ${state.phase || ''}\nstory: ${state.story || ''}`
		}
	}
})();
