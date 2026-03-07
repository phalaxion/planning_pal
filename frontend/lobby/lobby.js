try {
    document.addEventListener('DOMContentLoaded', () => {
        const last = localStorage.getItem('planning_pal.lastName') || ''
        const el = document.getElementById('name')
        if (el && last) el.value = last
    })
} catch (e) { }

function randRoom() {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
    let s = ''
    for (let i = 0; i < 6; i++) s += chars[Math.floor(Math.random() * chars.length)]
    return s
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

function getName() {
    const v = document.getElementById('name').value.trim()
    try { localStorage.setItem('planning_pal.lastName', v) } catch (e) { }
    return encodeURIComponent(v || 'Player')
}

function renderRecent() {
    try {
        const arr = JSON.parse(localStorage.getItem('planning_pal.recent') || '[]')
        const el = document.getElementById('recent-list')
        el.innerHTML = ''
        if (!arr.length) {
            el.innerHTML = '<span class="no-content">No recent rooms yet</span>'
            return
        }
        arr.forEach(r => {
            const b = document.createElement('button')
            b.className = 'recent-chip'
            b.innerHTML = `<span>↩</span>${r}`
            b.addEventListener('click', () => {
                location.href = `/room/${encodeURIComponent(r)}?name=${getName()}`
            })
            el.appendChild(b)
        })
    } catch (e) { }
}

document.addEventListener('DOMContentLoaded', renderRecent)

document.getElementById('create').addEventListener('click', async () => {
    const name = getName()
    const room = randRoom()
    saveRecent(room)
    location.href = `/room/${room}?name=${name}`
})

document.getElementById('join').addEventListener('click', () => {
    const room = document.getElementById('room').value.trim().toUpperCase()
    if (!room) { document.getElementById('room').focus(); return }
    saveRecent(room)
    location.href = `/room/${encodeURIComponent(room)}?name=${getName()}`
})

document.getElementById('room').addEventListener('input', function () {
    const pos = this.selectionStart
    this.value = this.value.toUpperCase()
    this.setSelectionRange(pos, pos)
})

document.getElementById('room').addEventListener('keydown', e => {
    if (e.key === 'Enter') document.getElementById('join').click()
})

renderRecent()

const error = new URLSearchParams(location.search).get('error')
if (error) {
    const message = new URLSearchParams(location.search).get('message')

    const msg = document.createElement('div')
    msg.style.cssText = 'margin-bottom:16px;padding:10px 14px;border-radius:8px;background:#fef2f2;border:1px solid rgba(185,28,28,0.15);color:#b91c1c;font-size:13px;font-weight:500'
    msg.textContent = message
    document.querySelector('.card').prepend(msg)
}