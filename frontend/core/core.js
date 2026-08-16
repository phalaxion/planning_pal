function qs(selector) { return document.querySelector(selector) }
function qsa(selector) { return document.querySelectorAll(selector) }

function getClientId() {
	let clientId = sessionStorage.getItem('planning_pal.clientId');
	if (clientId) {
		return clientId;
	}

	clientId = window.crypto?.randomUUID?.() || ('id-' + Math.random().toString(36).slice(2, 10));
	
	try {
		sessionStorage.setItem('planning_pal.clientId', clientId) 
	} catch (e) { };

	return clientId;
}

function getName() {
	const params = new URLSearchParams(location.search);
	const nameParam = params.get('name');
	return nameParam;
}

function getRoom() {
	const path = location.pathname.split('/');
	const roomId = path[2];
	return roomId;
}

function getDebug() {
	const params = new URLSearchParams(location.search);
	const showDebug = params.get('debug') === 'true';
	return showDebug;
}

// ── Status area ────────────────────────────────────────────────────────────
// #status carries two kinds of message. Connection state is sticky: it stays up
// until the connection changes. Errors are transient and clear themselves,
// restoring whatever connection state was underneath — otherwise a five second
// error would wipe a "reconnecting" notice the user still needs to see.

const statusState = { connection: null, errorTimer: null };

function renderStatus(title, detail, action) {
	const el = qs('#status');
	if (!el) return;

	el.innerHTML = '';

	if (!title) {
		el.style.display = 'none';
		return;
	}

	const heading = document.createElement('b');
	heading.textContent = detail ? `${title}: ` : title;
	el.appendChild(heading);

	if (detail) {
		el.appendChild(document.createTextNode(detail));
	}

	if (action) {
		const btn = document.createElement('button');
		btn.className = 'btn btn-primary';
		btn.style.cssText = 'margin-left:10px;padding:2px 10px;font-size:11px';
		btn.textContent = action.label;
		btn.onclick = action.onClick;
		el.appendChild(btn);
	}

	el.style.display = 'inline-block';
}

function renderConnectionState() {
	const c = statusState.connection;

	if (!c) {
		renderStatus(null);
		return;
	}

	renderStatus(c.type, c.message, c.onRetry ? { label: 'Reconnect', onClick: c.onRetry } : null);
}

function showConnectionStatus(type, message, onRetry) {
	statusState.connection = type === 'Connected' ? null : { type, message, onRetry };

	// Don't stomp on an error that is still being read.
	if (statusState.errorTimer) return;

	renderConnectionState();
}

// showStatusError surfaces a non-fatal server error without blocking the page.
// Named to avoid colliding with the lobby's own showError, since both live in
// the same global scope.
function showStatusError(message) {
	clearTimeout(statusState.errorTimer);
	renderStatus('Error', message);

	statusState.errorTimer = setTimeout(() => {
		statusState.errorTimer = null;
		renderConnectionState();
	}, 5000);
}