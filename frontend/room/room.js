(function () {
  
  const deck = ['0', '0.5', '1', '2', '3', '5', '8', '13', '21', '999', '?', '☕']

  const path = location.pathname.split('/')
  const roomId = path[2]
  const params = new URLSearchParams(location.search)
  const nameParam = params.get('name')
  const showDebug = params.get('debug') === '1' || params.get('debug') === 'true'

  const roomcodeEl = qs('#roomcode')
  roomcodeEl.textContent = roomId
  roomcodeEl.style.cursor = 'pointer'
  roomcodeEl.title = 'Click to copy invite link'
  roomcodeEl.addEventListener('click', () => {
    const url = new URL(window.location.href);
    navigator.clipboard.writeText(url.origin + url.pathname).then(() => {
      roomcodeEl.textContent = '✓ Copied'
      setTimeout(() => roomcodeEl.textContent = roomId, 1500)
    })
  })

  let ws = null
  let attempts = 0
  const maxAttempts = 5
  let sendQueue = []
  const nameKey = `planning_pal.name`
  let clientId = sessionStorage.getItem('planning_pal.clientId')
  let name = nameParam || sessionStorage.getItem('planning_pal.name') || localStorage.getItem('planning_pal.lastName')

  if (!name) {
    const errorMessage = "A name must be provided to join a room"
    location.href = `/?error=missing_name&message=${encodeURIComponent(errorMessage)}`
    return;
  }

  let __storyPrompted = false

  function ensureClientId() {
    if (clientId) return clientId
    clientId = window.crypto?.randomUUID?.() || ('id-' + Math.random().toString(36).slice(2, 10))
    try { sessionStorage.setItem(clientKey, clientId) } catch (e) { }
    return clientId
  }

  function setStatus(text) {
    const el = qs('#status')

    if (el) {
      el.textContent = text
      el.style.display = text ? 'inline-block' : 'none'
    }
  }

  function saveRecent(room) {
    try {
      const key = 'planning_pal.recent'
      const arr = JSON.parse(localStorage.getItem(key) || '[]')
      const idx = arr.indexOf(room)
      if (idx !== -1) arr.splice(idx, 1)
      arr.unshift(room)
      localStorage.setItem(key, JSON.stringify(arr.slice(0, 3)))
    } catch (e) { }
  }

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

  function connect() {
    const id = ensureClientId()
    try {
      sessionStorage.setItem(nameKey, name)
      localStorage.setItem(globalNameKey, name)
    } catch (e) { }

    const url = `${location.protocol.replace('http', 'ws')}//${location.host}/ws?room=${roomId}&name=${encodeURIComponent(name)}&clientId=${encodeURIComponent(id)}`
    ws = new WebSocket(url)

    ws.onopen = () => {
      attempts = 0
      setStatus('')
      saveRecent(roomId)
      while (sendQueue.length) ws.send(sendQueue.shift())
    }

    // hide debug panel unless requested
    const dbgEl = qs('#debug')
    if (dbgEl && !showDebug) dbgEl.style.display = 'none'

    ws.onmessage = (ev) => {
      const msg = JSON.parse(ev.data)
      const payload = (typeof msg.payload === 'string') ? JSON.parse(msg.payload) : msg.payload
      if (msg.type === 'state_update') renderRoom(payload)
      if (msg.type === 'error') {
        console.error('server error', payload)
        if (payload.fatal === "Yes") {
          location.href = `/?error=${payload.code}&message=${encodeURIComponent(payload.message)}`
        }
        else {
          alert(`Error: ${payload.message}`)
        }
      }
    }

    ws.onclose = () => attemptReconnect()
    ws.onerror = () => { } // close will trigger reconnect
  }

  function attemptReconnect() {
    if (attempts >= maxAttempts) {
      setStatus('Disconnected — reconnection failed')
      return
    }
    attempts++
    const backoff = Math.min(1000 * Math.pow(2, attempts - 1), 16000)
    setStatus(`Reconnecting… (${attempts}/${maxAttempts})`)
    setTimeout(connect, backoff)
  }

  function send(type, payload) {
    const m = JSON.stringify({ type, payload })
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      sendQueue.push(m)
    } else {
      ws.send(m)
    }
  }

  function renderRoom(state) {
    const youId = state.youId
    const isFac = state.facilitatorId && youId && state.facilitatorId === youId

    if (isFac && !state.story && !__storyPrompted) {
      __storyPrompted = true
      setTimeout(async () => {
        const story = await showStoryModal()
        if (story) send('set_story', { story })
      }, 300)
    }

    // ── Story ──────────────────────────────────────────────────
    const storyEl = qs('#story')
    if (storyEl && storyEl.textContent != state.story) {
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
        send('vote', { card })
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
    revealBtn.onclick = () => send('reveal')
    actions.appendChild(revealBtn)

    const newRoundBtn = document.createElement('button')
    newRoundBtn.className = 'btn btn-secondary'
    newRoundBtn.innerHTML = '↺ New round'
    newRoundBtn.disabled = !isFac
    if (!isFac) newRoundBtn.style.opacity = '0.4'
    newRoundBtn.onclick = async () => {
      const story = await showStoryModal()
      if (story !== null) {
        const nums = state.participants
          .map(p => p.vote)
          .filter(v => v && v !== '' && v !== '-' && v !== '?' && v !== '☕')
          .map(v => Number(v))
          .filter(n => isFinite(n))
        const lastRoundAverage = nums.length ? nums.reduce((a, b) => a + b, 0) / nums.length : 0

        send('new_round', { story, lastRoundAverage })
      }
    }
    actions.appendChild(newRoundBtn)

    const exportBtn = document.createElement('button')
    exportBtn.className = 'btn btn-ghost'
    exportBtn.textContent = '↓ Export CSV'
    exportBtn.onclick = () => {
      const rows = [['Story', 'Timestamp', ...(state.history[0] ? Object.keys(state.history[0].votes) : [])]]
      state.history.forEach(h => rows.push([h.story, new Date(h.timestamp).toLocaleString(), ...Object.values(h.votes || {})]))
      const csv = rows.map(r => r.map(c => `"${c}"`).join(',')).join('\n')
      const a = Object.assign(document.createElement('a'), { href: 'data:text/csv;charset=utf-8,' + encodeURIComponent(csv), download: `poker-${roomId}.csv` })
      a.click()
    }
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
          send('set_story', { story: val })
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
        .filter(v => v && v !== '' && v !== '-' && v !== '?' && v !== '☕')
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

    // ── History ────────────────────────────────────────────────
    const histEl = qs('#history')
    histEl.innerHTML = ''

    if (state.history && state.history.length) {
      state.history.slice().reverse().forEach(h => {
        const div = document.createElement('div')
        div.className = 'history-item'

        const storyLine = document.createElement('div')
        storyLine.className = 'history-story'
        storyLine.textContent = h.story || '(no story)'
        div.appendChild(storyLine)

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
    } else {
      const empty = document.createElement('div')
      empty.className = 'no-content'
      empty.textContent = 'No rounds completed yet.'
      histEl.appendChild(empty)
    }

    // ── Debug ──────────────────────────────────────────────────
    const dbg = qs('#debug')
    if (dbg && showDebug) {
      dbg.textContent = `youId: ${youId}\nfacilitatorId: ${state.facilitatorId || ''}\nphase: ${state.phase || ''}\nstory: ${state.story || ''}`
    }
  }

  connect()
})();
