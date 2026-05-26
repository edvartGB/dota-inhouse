document.addEventListener('DOMContentLoaded', () => {
    if ('serviceWorker' in navigator) {
        navigator.serviceWorker.register('/sw.js')
            .then(reg => {
                console.log('Service worker registered');
                subscribeToPush();
            });
    }

    // Check if running as standalone PWA
    const isStandalone = window.matchMedia('(display-mode: standalone)').matches
        || window.navigator.standalone
        || document.referrer.includes('android-app://');

    if (isStandalone) {
        console.log('Running in standalone mode');
        requestNotificationPermission();
    }
});

// Play notification sound when HTMX loads an element with data-play-notification
document.body.addEventListener('htmx:load', function(event) {
    const target = event.detail && event.detail.elt;
    if (!target) return;

    // Only trigger when the marked match-found container itself is swapped in.
    if (target.matches('[data-play-notification="true"]')) {
        triggerMatchFoundNotification('htmx-load');
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

function playNotificationSound() {
    notificationAudio.currentTime = 0;
    console.log("Playing notification sound");
    notificationAudio.play().catch(e => {
        console.warn("Audio play failed:", e);
    });
}

function triggerMatchFoundNotification(source) {
    playNotificationSound();
    console.log(`Match-found notification triggered (${source})`);
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
            console.error('Failed to send subscription to server:', response.status, text);
            return false;
        }
    } catch (error) {
        console.error('Error sending subscription to server:', error);
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

setInterval(function() {
    document.querySelectorAll('.countdown[data-deadline]').forEach(function(el) {
        var remaining = Math.max(0, Math.floor((new Date(el.dataset.deadline) - Date.now()) / 1000));
        var m = Math.floor(remaining / 60);
        var s = remaining % 60;
        el.textContent = m + ':' + (s < 10 ? '0' : '') + s;
        el.classList.toggle('countdown-urgent', remaining <= 10);
    });
}, 1000);

function isTypingTarget(target) {
    if (!target) return false;
    const tagName = target.tagName;
    return target.isContentEditable
        || tagName === "INPUT"
        || tagName === "TEXTAREA"
        || tagName === "SELECT";
}

document.addEventListener("keydown", function(event) {
    const isAcceptKey = event.key === "Enter" || event.key === " " || event.key === "Spacebar";
    if (!isAcceptKey || event.repeat || isTypingTarget(event.target)) return;

    const acceptButton = document.getElementById("accept-btn");
    const acceptDialog = document.getElementById("accept-dialog");
    if (!acceptButton || !acceptDialog || !acceptDialog.contains(acceptButton) || acceptButton.disabled) return;

    event.preventDefault();
    acceptButton.click();
});

// Keep accept button latched after first successful click to avoid duplicate taps
// while waiting for SSE UI update from server.
document.body.addEventListener('htmx:beforeRequest', function(event) {
    const elt = event.detail && event.detail.elt;
    if (!elt || elt.id !== 'accept-btn') return;

    elt.dataset.originalLabel = elt.textContent;
    elt.textContent = 'Accepting...';
    elt.disabled = true;
});

document.body.addEventListener('htmx:afterRequest', function(event) {
    const elt = event.detail && event.detail.elt;
    if (!elt || elt.id !== 'accept-btn') return;

    if (event.detail && event.detail.successful) {
        // Keep disabled until SSE swaps in the accepted button state.
        elt.textContent = 'Accepting...';
        elt.disabled = true;
        return;
    }

    // Request failed: restore button so user can retry.
    elt.textContent = elt.dataset.originalLabel || 'Accept Match';
    elt.disabled = false;
});
