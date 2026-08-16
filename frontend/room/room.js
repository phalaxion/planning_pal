import Connection from "../core/Connection.js";

(function () {
	// The deck is deployment configuration, sent by the server before the first
	// state update. Deliberately not defaulted here — one source of truth beats
	// two copies that drift.
	let deck = [];

	const clientId = getClientId();
	const name = getName();
	const roomId = getRoom();

	// Round history arrives on its own message rather than with every state
	// update, so it is held here between renders.
	let history = [];

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
			else if (msg.type === 'config') {
				deck = Array.isArray(payload.deck) ? payload.deck : []
			}
			else if (msg.type === 'history_update') {
				history = Array.isArray(payload.history) ? payload.history : []
				renderHistory()
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

	let __storyPrompted = false;

	function showStoryModal(defaultVal = '') {
		return new Promise(resolve => {
			const modal = qs('#story-modal')
			const input = qs('#modal-story-input')
			input.value = defaultVal
			modal.style.display = 'flex'
			input.focus()

			function confirm() {
				modal.style.display = 'none'
				resolve(input.value.trim())
				cleanup()
			}
			function cancel() {
				modal.style.display = 'none'
				resolve(null)
				cleanup()
			}
			function cleanup() {
				qs('#modal-confirm').removeEventListener('click', confirm)
				qs('#modal-cancel').removeEventListener('click', cancel)
				modal.removeEventListener('click', onBackdrop)
				input.removeEventListener('keydown', onKey)
			}
			function onBackdrop(e) { if (e.target === modal) cancel() }
			function onKey(e) {
				if (e.key === 'Enter') confirm()
				if (e.key === 'Escape') cancel()
			}

			qs('#modal-confirm').addEventListener('click', confirm)
			qs('#modal-cancel').addEventListener('click', cancel)
			modal.addEventListener('click', onBackdrop)
			input.addEventListener('keydown', onKey)
		})
	}

	// formatAverage renders a stored round average for display.
	//
	// A round records the average the room computed at the time, and stores 0
	// when no numeric votes were cast — everyone played '?' or '☕'. Since no
	// default card face is 0, a zero is read as "no average" rather than shown
	// as an average of zero.
	function formatAverage(value) {
		const n = Number(value)
		if (!isFinite(n) || n === 0) return '—'
		return String(Math.round(n * 10) / 10)
	}

	// ── History ────────────────────────────────────────────────────
	// Driven by history_update, not by renderRoom, so a vote no longer redraws
	// every round the room has ever played.
	function renderHistory() {
		const histEl = qs('#history')
		if (!histEl) return

		histEl.innerHTML = ''

		if (!history.length) {
			const empty = document.createElement('div')
			empty.className = 'no-content'
			empty.textContent = 'No rounds completed yet.'
			histEl.appendChild(empty)
			return
		}

		history.slice().reverse().forEach(h => {
			const div = document.createElement('div')
			div.className = 'history-item'

			const head = document.createElement('div')
			head.className = 'history-head'

			const storyLine = document.createElement('div')
			storyLine.className = 'history-story'
			storyLine.textContent = h.story || '(no story)'
			head.appendChild(storyLine)

			const avg = formatAverage(h.average_vote)
			const avgEl = document.createElement('div')
			avgEl.className = 'history-average' + (avg === '—' ? ' is-empty' : '')
			avgEl.textContent = avg
			avgEl.title = 'Average of the numeric votes in this round'
			head.appendChild(avgEl)

			div.appendChild(head)

			const meta = document.createElement('div')
			meta.className = 'history-meta'
			meta.textContent = new Date(h.timestamp).toLocaleString()
			div.appendChild(meta)

			const votes = document.createElement('div')
			votes.className = 'history-votes'
			votes.textContent = Object.entries(h.votes || {}).map(([n, v]) => `${n}: ${v}`).join('  ·  ')
			div.appendChild(votes)

			histEl.appendChild(div)
		})
	}

	// ── Export ─────────────────────────────────────────────────────
	// Exports only the rounds currently in memory. The server sends a capped
	// window, so this is deliberately partial — the button says so.
	function exportRecentRounds() {
		if (!history.length) return

		// Who was in the room changes between rounds, and people who did not vote
		// are not recorded at all. So the columns are the union of every name
		// seen, and each cell is filled by lookup rather than by position.
		const names = []
		history.forEach(h => Object.keys(h.votes || {}).forEach(n => {
			if (!names.includes(n)) names.push(n)
		}))
		names.sort((a, b) => a.localeCompare(b))

		const rows = [['Story', 'Timestamp', ...names]]
		history.forEach(h => rows.push([
			h.story || '',
			new Date(h.timestamp).toLocaleString(),
			...names.map(n => (h.votes || {})[n] ?? '')
		]))

		const csv = rows
			.map(r => r.map(cell => `"${String(cell).replace(/"/g, '""')}"`).join(','))
			.join('\n')

		const a = Object.assign(document.createElement('a'), {
			href: 'data:text/csv;charset=utf-8,' + encodeURIComponent(csv),
			download: `poker-${roomId}-recent.csv`
		})
		a.click()
	}

	function renderRoom(state) {
		const youId = state.youId
		const isFac = state.facilitatorId && youId && state.facilitatorId === youId

		if (isFac && !state.story && !__storyPrompted) {
			__storyPrompted = true
			setTimeout(async () => {
				const story = await showStoryModal()
				if (story) ws.send('set_story', { story })
			}, 300)
		}

		// ── Story ──────────────────────────────────────────────────
		const storyEl = qs('#story')
		if (storyEl && !window.__storyEditing && storyEl.textContent != state.story) {
			storyEl.textContent = state.story || 'No story set'
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

		// Vote display
		const voteEl = document.createElement('div')

		if (state.phase === 'revealed') {
			voteEl.className = 'p-vote'
			voteEl.textContent = pt.vote || '?'
		} else if (isYou) {
			voteEl.className = 'p-vote'
			voteEl.textContent = pt.vote || '-'
		} else if (voted) {
			voteEl.className = 'p-vote voted-hidden'
		} else {
			voteEl.className = 'p-vote waiting'
		}

		card.appendChild(voteEl)

		const nameEl = document.createElement('div')
		nameEl.className = 'p-name' + (isYou ? ' is-you' : '')
		nameEl.textContent = pt.name
		card.appendChild(nameEl)

		p.appendChild(card)
		})

		// ── Voting deck ────────────────────────────────────────────
		const deckEl = qs('#deck')
		deckEl.innerHTML = ''

		// find current user's vote to pre-highlight
		const myVote = participants.find(pt => pt.id === youId)?.vote || null

		deck.forEach(card => {
		const b = document.createElement('button')
		b.className = 'deck-card' + (card === myVote ? ' selected' : '')
		b.textContent = card
		b.onclick = () => {
			// optimistic highlight
			deckEl.querySelectorAll('.deck-card').forEach(el => el.classList.remove('selected'))
			b.classList.add('selected')
			ws.send('vote', { card })
		}
		deckEl.appendChild(b)
		})

		// ── Actions ────────────────────────────────────────────────
		const actions = qs('#actions')
		actions.innerHTML = ''

		const revealBtn = document.createElement('button')
		revealBtn.className = 'btn btn-primary'
		revealBtn.innerHTML = '⬡ Reveal cards'
		revealBtn.disabled = !isFac
		if (!isFac) revealBtn.style.opacity = '0.4'
		revealBtn.onclick = () => ws.send('reveal')
		actions.appendChild(revealBtn)

		const newRoundBtn = document.createElement('button')
		newRoundBtn.className = 'btn btn-secondary'
		newRoundBtn.innerHTML = '↺ New round'
		newRoundBtn.disabled = !isFac
		if (!isFac) newRoundBtn.style.opacity = '0.4'
			newRoundBtn.onclick = async () => {
			const story = await showStoryModal()
			if (story !== null) {
				// The server computes the round's average from the real votes. It
				// cannot be done here: during voting everyone else's vote reads
				// "hidden", so this client can only see its own.
				ws.send('new_round', { story })
			}
		}
		actions.appendChild(newRoundBtn)

		const exportBtn = document.createElement('button')
		exportBtn.className = 'btn btn-ghost'
		exportBtn.textContent = '↓ Export recent rounds'
		exportBtn.title = 'Exports the rounds shown below. Older rounds are kept on the server but cannot be exported yet.'
		exportBtn.onclick = exportRecentRounds
		actions.appendChild(exportBtn)

		actions.style.display = isFac ? 'flex' : 'none';

		// ── Story editing ──────────────────────────────────────────
		if (storyEl) {
			const editBtn = qs('#edit-story')
			const saveBtn = qs('#save-story')

			if (!('last' in storyEl.dataset)) storyEl.dataset.last = storyEl.textContent || ''

			function exitEditing(cancel) {
				window.__storyEditing = false

				if (cancel) {
				storyEl.textContent = storyEl.dataset.last || ''
				} else {
				storyEl.dataset.last = storyEl.textContent || ''
				}
				storyEl.contentEditable = 'false'
				if (editBtn) editBtn.style.display = 'block';
				if (saveBtn) saveBtn.style.display = 'none';
			}

			function performSave() {
				const val = (storyEl.textContent || '').trim()

				if (val !== (storyEl.dataset.last || '')) {
					ws.send('set_story', { story: val })
					storyEl.dataset.last = val
					state.story = val
				}

				exitEditing(false)
			}

			if (isFac) {
				if (!window.__storyEditing) {
				if (editBtn) editBtn.style.display = 'block';
				if (saveBtn) saveBtn.style.display = 'none';
				storyEl.contentEditable = 'false'
				} else {
				if (editBtn) editBtn.style.display = 'none';
				if (saveBtn) saveBtn.style.display = 'block';
				storyEl.contentEditable = 'true'
				storyEl.focus()

				const range = document.createRange();
				range.selectNodeContents(storyEl);
				range.collapse(false);

				const selection = window.getSelection();
				selection.removeAllRanges();
				selection.addRange(range);
				}

				if (!storyEl.dataset._listeners) {
				if (editBtn) editBtn.addEventListener('click', () => {
					window.__storyEditing = true
					storyEl.dataset.last = storyEl.textContent || ''

					renderRoom(state)
				})

				if (saveBtn) saveBtn.addEventListener('click', performSave)

				storyEl.addEventListener('keydown', e => {
					if (e.key === 'Enter') { e.preventDefault(); performSave() }
					if (e.key === 'Escape') { e.preventDefault(); exitEditing(true) }
				})

				storyEl.dataset._listeners = '1'
				}
			} else {
				window.__storyEditing = false
				if (editBtn) editBtn.style.display = 'none';
				if (saveBtn) saveBtn.style.display = 'none';
				storyEl.contentEditable = 'false'
			}
		}

		// ── Results summary ────────────────────────────────────────
		const resEl = qs('#results-summary');
		const consensusBadge = qs('#consensus-badge');
		if (state.phase === 'revealed') {
		const nums = state.participants
			.map(p => p.vote)
			.filter(v => v)
			.map(v => Number(v))
			.filter(n => isFinite(n))
		const avg = nums.length ? nums.reduce((a, b) => a + b, 0) / nums.length : null
		resEl.className = 'avg-value'
		resEl.textContent = avg !== null ? Math.round(avg * 10) / 10 : '—'

		const allSame = nums.length > 1 && nums.every(n => n === nums[0])
		consensusBadge.style.visibility = allSame ? 'visible' : 'hidden';
		} else {
		resEl.className = 'avg-value hidden-state'
		resEl.textContent = 'Hidden while voting'
		consensusBadge.style.visibility = 'hidden';
		}

		// ── Debug ──────────────────────────────────────────────────
		const dbg = qs('#debug')
		if (dbg && getDebug()) {
			dbg.style.display = 'block';
			dbg.textContent = `youId: ${youId}\nfacilitatorId: ${state.facilitatorId || ''}\nphase: ${state.phase || ''}\nstory: ${state.story || ''}`
		}
	}
})();
