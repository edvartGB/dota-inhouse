self.addEventListener('install', () => {
    self.skipWaiting();
});

self.addEventListener('activate', () => {
    self.clients.claim();
});

self.addEventListener('message', (event) => {
    if (event.data?.type === 'MATCH_FOUND') {
        self.registration.showNotification('DNDL Match found', {
            body: 'Click to accept your match.',
            icon: '/static/dota-icon.png',
            tag: 'match-found'
        });
    }
});

self.addEventListener('notificationclick', (event) => {
    event.notification.close();

    event.waitUntil(
        clients.matchAll({ type: 'window', includeUncontrolled: true })
            .then(clientList => {
                for (const client of clientList) {
                    if ('focus' in client) return client.focus();
                }
                return clients.openWindow('/');
            })
    );
});
