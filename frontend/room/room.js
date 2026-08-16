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

	// Pending queue items, from queue_update. The item being voted on is not in
	// here — the server leaves it out, because it isn't "next", it's now.
	let queue = [];
	let editingId = null;
	let isFacilitator = false;

	if (!name) {
		const errorMessage = "A name must be provided to join a room"
		location.href = `/?error=missing_name&message=${encodeURIComponent(errorMessage)}`
		return;
	}

	// Name the tab after the room — several rooms open at once is normal, and
	// they are otherwise indistinguishable.
	document.title = 'Planning Poker - ' + roomId

	const roomcodeEl = qs('#roomcode')
	roomcodeEl.textContent = roomId
	roomcodeEl.title = 'Click to copy invite link'
	roomcodeEl.addEventListener('click', () => {
		const url = new URL(window.location.href);
		navigator.clipboard.writeText(`${url.origin}/?room=${encodeURIComponent(roomId)}`).then(() => {
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
				showStatusError(payload.message)
			}
			else if (msg.type === 'state_update') {
				renderRoom(payload)
			}
			else if (msg.type === 'config') {
				deck = Array.isArray(payload.deck) ? payload.deck : []
			}
			else if (msg.type === 'queue_update') {
				queue = Array.isArray(payload.queue) ? payload.queue : []
				renderQueue()
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
			showConnectionStatus(type, message, type === 'Disconnected' ? () => ws.retry() : null)
		}
	);

	let __storyPrompted = false;
	let __firstRender = true;

	// showStoryModal asks what to estimate next. It resolves with {itemId} when a
	// queued item is started, {story} when one is typed, or null on cancel.
	function showStoryModal(defaultVal = '', title = 'New round', confirmLabel = 'Start round →') {
		return new Promise(resolve => {
			const modal = qs('#story-modal')
			const input = qs('#modal-story-input')

			// The queued items, each startable directly.
			const picker = qs('#modal-queue')
			picker.innerHTML = ''
			queue.forEach(item => {
				picker.appendChild(queueRow(item, [
					{ label: 'Start →', primary: true, onClick: i => { cleanup(); modal.style.display = 'none'; resolve({ itemId: i.id }) } }
				]))
			})

			// The same modal asks two different questions — "what are we
			// estimating next" and "this room has no story yet" — so it says
			// which one it is.
			const titleEl = qs('#modal-title')
			if (titleEl) titleEl.textContent = title
			qs('#modal-confirm').textContent = confirmLabel

			input.value = defaultVal
			modal.style.display = 'flex'
			input.focus()

			function confirm() {
				modal.style.display = 'none'
				resolve({ story: input.value.trim() })
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

	// ── Queue ──────────────────────────────────────────────────────
	// appendLinkified writes text into an element, turning URLs into real links.
	//
	// Nodes are built rather than an HTML string assembled. Notes are typed by
	// one person and rendered by everyone in the room, so innerHTML here would
	// be a stored XSS delivered to the whole team. Only http(s) is matched, so
	// no javascript: URL can reach an href.
	function appendLinkified(el, text) {
		const pattern = /https?:\/\/[^\s<>"')]+/g
		let last = 0
		let match

		while ((match = pattern.exec(text)) !== null) {
			if (match.index > last) {
				el.appendChild(document.createTextNode(text.slice(last, match.index)))
			}

			const a = document.createElement('a')
			a.href = match[0]
			a.target = '_blank'
			a.rel = 'noopener noreferrer'
			a.textContent = match[0]
			el.appendChild(a)

			last = match.index + match[0].length
		}

		if (last < text.length) {
			el.appendChild(document.createTextNode(text.slice(last)))
		}
	}

	function renderStoryNotes(notes) {
		const el = qs('#story-notes')
		if (!el) return

		el.innerHTML = ''

		if (!notes) {
			el.style.display = 'none'
			return
		}

		appendLinkified(el, notes)
		el.style.display = 'block'
	}

	function queueRow(item, opts) {
		const row = document.createElement('div')
		row.className = 'queue-row'

		const main = document.createElement('div')
		main.className = 'queue-row-main'

		const title = document.createElement('div')
		title.className = 'queue-row-title'
		title.textContent = item.title
		main.appendChild(title)

		if (item.notes) {
			const notes = document.createElement('div')
			notes.className = 'queue-row-notes'
			appendLinkified(notes, item.notes)
			main.appendChild(notes)
		}

		row.appendChild(main)

		const actions = document.createElement('div')
		actions.className = 'queue-row-actions'

		opts.forEach(action => {
			const btn = document.createElement('button')
			btn.className = action.primary ? 'btn btn-primary' : 'btn btn-ghost'
			btn.textContent = action.label
			btn.onclick = () => action.onClick(item)
			actions.appendChild(btn)
		})

		row.appendChild(actions)

		return row
	}

	// renderQueue draws the management list inside the queue modal.
	function renderQueue() {
		const list = qs('#queue-list')
		if (!list) return

		list.innerHTML = ''

		if (!queue.length) {
			const empty = document.createElement('div')
			empty.className = 'queue-empty'
			empty.textContent = 'Nothing queued yet. Add things during the day and they will be here in the morning.'
			list.appendChild(empty)
			return
		}

		queue.forEach(item => {
			// Anyone can add; only the facilitator can change or remove what is
			// already there.
			const actions = isFacilitator ? [
				{ label: 'Edit', onClick: beginEdit },
				{ label: 'Remove', onClick: i => ws.send('queue_remove', { id: i.id }) }
			] : []

			list.appendChild(queueRow(item, actions))
		})
	}

	function beginEdit(item) {
		editingId = item.id
		qs('#queue-title').value = item.title
		qs('#queue-notes').value = item.notes || ''
		qs('#queue-add').textContent = 'Save changes'
		qs('#queue-title').focus()
	}

	function resetQueueForm() {
		editingId = null
		qs('#queue-title').value = ''
		qs('#queue-notes').value = ''
		qs('#queue-add').textContent = 'Add to queue'
	}

	function submitQueueForm() {
		const title = qs('#queue-title').value.trim()
		const notes = qs('#queue-notes').value.trim()

		if (!title) {
			qs('#queue-title').focus()
			return
		}

		if (editingId) {
			ws.send('queue_edit', { id: editingId, title, notes })
		} else {
			ws.send('queue_add', { title, notes })
		}

		resetQueueForm()
	}

	function openQueueModal() {
		renderQueue()
		resetQueueForm()
		qs('#queue-modal').style.display = 'flex'
		qs('#queue-title').focus()
	}

	function closeQueueModal() {
		resetQueueForm()
		qs('#queue-modal').style.display = 'none'
	}

	qs('#open-queue').addEventListener('click', openQueueModal)
	qs('#queue-close').addEventListener('click', closeQueueModal)
	qs('#queue-add').addEventListener('click', submitQueueForm)
	qs('#queue-modal').addEventListener('click', e => {
		if (e.target === qs('#queue-modal')) closeQueueModal()
	})
	qs('#queue-title').addEventListener('keydown', e => {
		if (e.key === 'Enter') { e.preventDefault(); submitQueueForm() }
		if (e.key === 'Escape') { e.preventDefault(); closeQueueModal() }
	})

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

		// The queue modal renders edit/remove based on this, and it is drawn from
		// its own message, so keep it current here rather than passing state down.
		if (isFacilitator !== !!isFac) {
			isFacilitator = !!isFac
			renderQueue()
		}

		renderStoryNotes(state.storyNotes)

		// Prompt for a story only when arriving into an empty room. Inheriting
		// the role later — a handover, or the grace period expiring — used to
		// throw this modal over someone who was quietly participating; they can
		// use Edit instead.
		if (__firstRender && isFac && !state.story && !__storyPrompted) {
			__storyPrompted = true
			setTimeout(async () => {
				const choice = await showStoryModal('', 'Set the story', 'Set story →')
				if (!choice) return

				// Picking a queued item has to go through new_round so the server
				// knows which item backs the story; typing one does not.
				if (choice.itemId) {
					ws.send('new_round', { itemId: choice.itemId })
				} else if (choice.story) {
					ws.send('set_story', { story: choice.story })
				}
			}, 300)
		}
		__firstRender = false

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

		// How many have voted, so the facilitator isn't counting cards to decide
		// when to reveal. Pointless once revealed — everyone can see.
		const progressEl = qs('#vote-progress')
		if (progressEl) {
			const votedCount = participants.filter(pt => pt.voted).length
			progressEl.textContent = (state.phase === 'revealed' || !participants.length)
				? ''
				: `${votedCount} of ${participants.length} voted`
		}

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
		nameEl.title = pt.name // the card truncates; keep the full name reachable
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

		// While the facilitator's grace period runs the room has no facilitator,
		// so nobody sees the controls. Say why, rather than looking broken.
		if (state.awaitingFacilitator) {
			const waiting = document.createElement('div')
			waiting.className = 'facilitator-waiting'
			waiting.textContent = `Waiting for ${state.awaitingFacilitator} to reconnect…`
			actions.appendChild(waiting)
		}

		// Reveal and New round are the facilitator's to press, so they are only
		// rendered for them. Export is read-only and everyone gets it.
		if (isFac) {
		const revealBtn = document.createElement('button')
		revealBtn.className = 'btn btn-primary'
		revealBtn.innerHTML = '⬡ Reveal cards'
		revealBtn.onclick = () => ws.send('reveal')
		actions.appendChild(revealBtn)

		const newRoundBtn = document.createElement('button')
		newRoundBtn.className = 'btn btn-secondary'
		newRoundBtn.innerHTML = '↺ New round'
			newRoundBtn.onclick = async () => {
			const choice = await showStoryModal()
			if (choice !== null) {
				// The server computes the round's average from the real votes. It
				// cannot be done here: during voting everyone else's vote reads
				// "hidden", so this client can only see its own.
				ws.send('new_round', choice.itemId ? { itemId: choice.itemId } : { story: choice.story })
			}
		}
		actions.appendChild(newRoundBtn)
		}

		const exportBtn = document.createElement('button')
		exportBtn.className = 'btn btn-ghost'
		exportBtn.textContent = '↓ Export recent rounds'
		exportBtn.title = 'Exports the rounds shown below. Older rounds are kept on the server but cannot be exported yet.'
		exportBtn.onclick = exportRecentRounds
		actions.appendChild(exportBtn)

		actions.style.display = 'flex';

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
		const spreadEl = qs('#results-spread');
		if (state.phase === 'revealed') {
		const nums = state.participants
			.map(p => p.vote)
			.filter(v => v)
			.map(v => Number(v))
			.filter(n => isFinite(n))
		const avg = nums.length ? nums.reduce((a, b) => a + b, 0) / nums.length : null
		resEl.className = 'avg-value'
		resEl.textContent = avg !== null ? Math.round(avg * 10) / 10 : '—'

		// The rounded average is the number the team actually takes as the
		// estimate, so show it rather than making everyone do it in their head.
		const roundedWrap = qs('#results-rounded-wrap')
		if (avg !== null) {
			qs('#results-rounded').textContent = Math.round(avg)
			roundedWrap.style.display = 'block'
		} else {
			roundedWrap.style.display = 'none'
		}

		// An average of two numbers out of six people looks like more agreement
		// than it is, so say how many votes are actually behind it.
		const captionEl = qs('#results-caption')
		if (captionEl) {
			const cast = state.participants.filter(p => p.voted).length
			captionEl.textContent = !nums.length
				? 'no numeric votes'
				: nums.length === cast
					? `from ${nums.length} vote${nums.length === 1 ? '' : 's'}`
					: `from ${nums.length} of ${cast} votes`
		}

		// The average alone hides disagreement — 1,3,13 and 5,6,6 average about
		// the same, and only one of them is worth talking about.
		if (nums.length) {
			qs('#results-low').textContent = Math.min(...nums)
			qs('#results-high').textContent = Math.max(...nums)
			spreadEl.style.display = 'flex'
		} else {
			spreadEl.style.display = 'none'
		}

		const allSame = nums.length > 1 && nums.every(n => n === nums[0])
		consensusBadge.style.visibility = allSame ? 'visible' : 'hidden';
		} else {
		resEl.className = 'avg-value hidden-state'
		resEl.textContent = 'Hidden while voting'
		consensusBadge.style.visibility = 'hidden';
		spreadEl.style.display = 'none';
		qs('#results-rounded-wrap').style.display = 'none';
		const captionEl = qs('#results-caption')
		if (captionEl) captionEl.textContent = ''
		}

		// ── Debug ──────────────────────────────────────────────────
		const dbg = qs('#debug')
		if (dbg && getDebug()) {
			dbg.style.display = 'block';
			dbg.textContent = `youId: ${youId}\nfacilitatorId: ${state.facilitatorId || ''}\nphase: ${state.phase || ''}\nstory: ${state.story || ''}`
		}
	}
})();
