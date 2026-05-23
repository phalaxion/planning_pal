function qs(selector) { return document.querySelector(selector) }

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