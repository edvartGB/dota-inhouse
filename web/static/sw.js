const OLD_CACHES = [
    'dota-inhouse-v1',
    'dota-inhouse-static-v1'
];

self.addEventListener('install', (event) => {
    self.skipWaiting();
});

self.addEventListener('activate', (event) => {
    event.waitUntil(
        caches.keys()
            .then(cacheNames => Promise.all(
                cacheNames
                    .filter(name => OLD_CACHES.includes(name))
                    .map(name => caches.delete(name))
            ))
            .then(() => self.clients.claim())
    );
});

// Handle push events from server
self.addEventListener('push', (event) => {
    console.log('🔔 Push event received!', {
        hasData: !!event.data,
        timestamp: new Date().toISOString()
    });

    let notification = {
        title: 'DNDL Match Found! 🎮',
        body: 'Click to accept your match.',
        icon: '/static/favicon.ico',
        badge: '/static/favicon.ico',
        tag: 'match-found',
        requireInteraction: true,
        vibrate: [200, 100, 200],
        data: { url: '/' }
    };

    // Parse push data if available
    if (event.data) {
        try {
            const data = event.data.json();
            console.log('Push data:', data);
            notification = {
                ...notification,
                title: data.title || notification.title,
                body: data.body || notification.body,
                icon: data.icon || notification.icon,
                badge: data.badge || notification.badge,
                tag: data.tag || notification.tag,
                data: data.data || notification.data
            };
        } catch (e) {
            console.error('Failed to parse push data:', e);
            // If no JSON, try to get as text
            try {
                const text = event.data.text();
                console.log('Push text data:', text);
            } catch (e2) {
                console.error('Failed to parse push text:', e2);
            }
        }
    }

    const showNotificationPromise = self.registration.showNotification(notification.title, {
        body: notification.body,
        icon: notification.icon,
        badge: notification.badge,
        tag: notification.tag,
        requireInteraction: notification.requireInteraction,
        vibrate: notification.vibrate,
        data: notification.data,
        silent: false  // Ensure notification plays default system sound
    }).then(() => {
        console.log('✅ Push notification shown successfully');
    }).catch((err) => {
        console.error('❌ Failed to show push notification:', err);
        throw err; // Re-throw to mark the event as failed
    });

    event.waitUntil(showNotificationPromise);
});

// Handle notification clicks
self.addEventListener('notificationclick', (event) => {
    event.notification.close();

    const urlToOpen = new URL(event.notification.data?.url || '/', self.location.origin).href;

    event.waitUntil(
        clients.matchAll({ type: 'window', includeUncontrolled: true })
            .then(clientList => {
                // Look for an existing window with matching URL
                for (const client of clientList) {
                    if (client.url === urlToOpen && 'focus' in client) {
                        return client.focus();
                    }
                }
                // If no matching window found, open a new one
                if (clients.openWindow) {
                    return clients.openWindow(urlToOpen);
                }
            })
    );
});

// Handle push subscription changes (e.g., when subscription expires)
self.addEventListener('pushsubscriptionchange', (event) => {
    console.log('⚠️ Push subscription changed/expired');

    event.waitUntil(
        fetch('/api/push/vapid-public-key')
            .then(response => response.json())
            .then(data => {
                const vapidPublicKey = data.publicKey;

                // Re-subscribe
                return self.registration.pushManager.subscribe({
                    userVisibleOnly: true,
                    applicationServerKey: urlBase64ToUint8Array(vapidPublicKey)
                });
            })
            .then(subscription => {
                console.log('✅ Re-subscribed to push notifications');

                // Send new subscription to server
                return fetch('/api/push/subscribe', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                    },
                    body: JSON.stringify(subscription.toJSON())
                });
            })
            .then(() => {
                console.log('✅ New subscription sent to server');
            })
            .catch(err => {
                console.error('❌ Failed to re-subscribe:', err);
            })
    );
});

// Helper function for VAPID key conversion (needed in service worker context)
function urlBase64ToUint8Array(base64String) {
    const padding = '='.repeat((4 - base64String.length % 4) % 4);
    const base64 = (base64String + padding)
        .replace(/\-/g, '+')
        .replace(/_/g, '/');

    const rawData = atob(base64);
    const outputArray = new Uint8Array(rawData.length);

    for (let i = 0; i < rawData.length; ++i) {
        outputArray[i] = rawData.charCodeAt(i);
    }
    return outputArray;
}
