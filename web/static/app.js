let eventSource = null;
let reconnectTimeout = null;
let audioUnlocked = false;
let reconnectAttempts = 0;

document.addEventListener('DOMContentLoaded', () => {
    connectSSE();

    if ('serviceWorker' in navigator) {
        navigator.serviceWorker.register('/sw.js')
            .then(reg => {
                console.log('Service worker registered');
                // Subscribe to push notifications after service worker is ready
                subscribeToPush();
            });
    }

    // Check if running as standalone PWA
    const isStandalone = window.matchMedia('(display-mode: standalone)').matches
        || window.navigator.standalone
        || document.referrer.includes('android-app://');

    if (isStandalone) {
        console.log('Running in standalone mode');
        // Request notification permission proactively for PWA
        requestNotificationPermission();
    }
});


const notificationAudio = new Audio('/static/faceit_trumpet.mp3');
notificationAudio.load();
notificationAudio.volume = 0.7;
notificationAudio.preload = 'auto';


function requestNotificationPermission() {
    if (!('Notification' in window)) {
        console.warn('Notifications not supported');
        return;
    }

    console.log('Current notification permission:', Notification.permission);

    if (Notification.permission === 'default') {
        Notification.requestPermission().then(permission => {
            console.log('Notification permission:', permission);
            if (permission === 'denied') {
                alert('Please enable notifications in your browser/app settings to receive match alerts!');
            }
        });
    }
}

function unlockAudio() {
    if (audioUnlocked) return;

    notificationAudio.play()
        .then(() => {
            notificationAudio.pause();
            notificationAudio.currentTime = 0;
            audioUnlocked = true;
            console.log("Audio unlocked");
        })
        .catch(() => { });

    // Also request notification permission on interaction
    requestNotificationPermission();
}

document.addEventListener('click', unlockAudio, { once: true });
document.addEventListener('keydown', unlockAudio, { once: true });
document.addEventListener('touchstart', unlockAudio, { once: true });

function playNotificationSound() {
    if (!audioUnlocked) return;

    notificationAudio.currentTime = 0;
    console.log("Playing notification sound");
    notificationAudio.play().catch(e => {
        console.warn("Audio play failed:", e);
    });
}

function connectSSE() {
    if (eventSource) {
        eventSource.close();
    }

    eventSource = new EventSource('/events');

    eventSource.onopen = () => {
        console.log('SSE connected');
        reconnectAttempts = 0;
        if (reconnectTimeout) {
            clearTimeout(reconnectTimeout);
            reconnectTimeout = null;
        }
    };


    eventSource.onmessage = (event) => {
        const container = document.createElement('div');
        container.innerHTML = event.data;

        container.querySelectorAll('[hx-swap-oob]').forEach(el => {
            const targetId = el.id;
            const target = document.getElementById(targetId);

            if (el.dataset.playNotification === 'true') {
                playNotificationSound();
                notifyMatchFound();
            }

            if (target) {
                target.replaceWith(el);
            } else {
                document.body.appendChild(el);
            }
            el.removeAttribute('hx-swap-oob');

            if (typeof htmx !== 'undefined') {
                htmx.process(el);
            }
        });
    };

    eventSource.onerror = (err) => {
        console.error('SSE error:', err);
        eventSource.close();

        reconnectAttempts++;
        const delay = Math.min(1000 * Math.pow(2, reconnectAttempts - 1), 30000);

        console.log(`SSE disconnected. Reconnecting in ${delay}ms (attempt ${reconnectAttempts})...`);

        reconnectTimeout = setTimeout(() => {
            connectSSE();
        }, delay);
    };
}

// Connect SSE when page loads
document.addEventListener('DOMContentLoaded', () => {
    connectSSE();
});

// Cleanup on page unload
window.addEventListener('beforeunload', () => {
    if (eventSource) {
        eventSource.close();
    }
});


function notifyMatchFound() {
    console.log('notifyMatchFound called, permission:', Notification.permission);

    if (!('serviceWorker' in navigator)) {
        console.warn('Service Worker not supported');
        return;
    }

    if (Notification.permission !== 'granted') {
        console.warn('Notification permission not granted. Current:', Notification.permission);
        // Try to request permission again
        requestNotificationPermission();
        return;
    }

    navigator.serviceWorker.ready.then(reg => {
        if (reg.active) {
            console.log('Sending MATCH_FOUND message to service worker');
            reg.active.postMessage({ type: 'MATCH_FOUND' });
        } else {
            console.warn('Service worker not active');
        }
    }).catch(err => {
        console.error('Service worker error:', err);
    });
}


// Push notification subscription

async function subscribeToPush() {
    if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
        console.log('Push notifications not supported');
        return;
    }

    try {
        const registration = await navigator.serviceWorker.ready;

        // Check if already subscribed
        let subscription = await registration.pushManager.getSubscription();

        if (!subscription) {
            console.log('No existing push subscription, creating new one...');
            // Get VAPID public key from server
            const response = await fetch('/api/push/vapid-public-key');
            if (!response.ok) {
                console.error('Failed to get VAPID key:', response.status);
                return;
            }
            const data = await response.json();
            const vapidPublicKey = data.publicKey;

            console.log('Subscribing to push notifications...');

            // Subscribe to push
            subscription = await registration.pushManager.subscribe({
                userVisibleOnly: true,
                applicationServerKey: urlBase64ToUint8Array(vapidPublicKey)
            });

            console.log('Push subscription created:', subscription);
        } else {
            console.log('Existing push subscription found:', subscription.endpoint.substring(0, 50) + '...');
        }

        // Always send/update subscription to server to ensure it's current
        await sendSubscriptionToServer(subscription);

        // Set up periodic subscription check (every 5 minutes)
        setInterval(async () => {
            try {
                const currentSub = await registration.pushManager.getSubscription();
                if (!currentSub) {
                    console.warn('Push subscription lost, re-subscribing...');
                    await subscribeToPush();
                }
            } catch (err) {
                console.error('Error checking push subscription:', err);
            }
        }, 5 * 60 * 1000);

    } catch (error) {
        console.error('Failed to subscribe to push:', error);
        // Retry after 10 seconds
        setTimeout(subscribeToPush, 10000);
    }
}

async function sendSubscriptionToServer(subscription) {
    try {
        const subData = subscription.toJSON();
        console.log('Sending push subscription to server:', {
            endpoint: subData.endpoint.substring(0, 50) + '...',
            hasKeys: !!(subData.keys && subData.keys.p256dh && subData.keys.auth)
        });

        const response = await fetch('/api/push/subscribe', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(subData)
        });

        if (response.ok) {
            console.log('Push subscription successfully registered with server');
            return true;
        } else {
            const text = await response.text();
            console.error('❌ Failed to send subscription to server:', response.status, text);
            return false;
        }
    } catch (error) {
        console.error('❌ Error sending subscription to server:', error);
        return false;
    }
}

// Helper function to convert VAPID key
function urlBase64ToUint8Array(base64String) {
    const padding = '='.repeat((4 - base64String.length % 4) % 4);
    const base64 = (base64String + padding)
        .replace(/\-/g, '+')
        .replace(/_/g, '/');

    const rawData = window.atob(base64);
    const outputArray = new Uint8Array(rawData.length);

    for (let i = 0; i < rawData.length; ++i) {
        outputArray[i] = rawData.charCodeAt(i);
    }
    return outputArray;
}

// Test push notification
async function testPushNotification() {
    console.log('Testing push notification...');

    // First check subscription status
    await checkPushStatus();

    try {
        const response = await fetch('/api/push/test', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            }
        });

        if (response.ok) {
            const data = await response.json();
            console.log('Test notification sent:', data);
            alert('Test notification sent! Check if you received it.');
        } else {
            const text = await response.text();
            console.error('❌ Failed to send test notification:', response.status, text);
            alert(`Failed to send test notification: ${response.status} ${text}`);
        }
    } catch (error) {
        console.error('❌ Error testing push notification:', error);
        alert(`Error: ${error.message}`);
    }
}

// Check push notification status (for debugging)
async function checkPushStatus() {
    console.log('=== Push Notification Status ===');
    console.log('🔔 Notification permission:', Notification.permission);
    console.log('🔧 Service Worker support:', 'serviceWorker' in navigator);
    console.log('📬 Push Manager support:', 'PushManager' in window);

    if ('serviceWorker' in navigator) {
        try {
            const registration = await navigator.serviceWorker.ready;
            console.log('✅ Service Worker active:', !!registration.active);

            const subscription = await registration.pushManager.getSubscription();
            if (subscription) {
                console.log('✅ Push subscription exists');
                console.log('   Endpoint:', subscription.endpoint.substring(0, 60) + '...');
                console.log('   ExpirationTime:', subscription.expirationTime || 'Never');
            } else {
                console.log('❌ No push subscription found');
            }
        } catch (error) {
            console.error('❌ Error checking service worker:', error);
        }
    }
    console.log('================================');
}
