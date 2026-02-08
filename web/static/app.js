let eventSource = null;
let reconnectTimeout = null;
let audioUnlocked = false;

// Browsers block audio until a user gesture has occurred.
// Mark as unlocked on any interaction so playNotificationSound can fire.
function unlockAudio() {
    if (audioUnlocked) return;
    audioUnlocked = true;

    // Request notification permission on first interaction
    if ('Notification' in window && Notification.permission === 'default') {
        Notification.requestPermission();
    }
}
document.addEventListener('click', unlockAudio, { once: true });
document.addEventListener('keydown', unlockAudio, { once: true });
document.addEventListener('touchstart', unlockAudio, { once: true });

function playNotificationSound() {
    try {
        const audio = new Audio('/static/faceit_trumpet.mp3');
        audio.volume = 0.7;
        audio.play().catch(e => {
            console.warn('Could not play notification sound (may need user interaction first):', e);
        });
    } catch (e) {
        console.warn('Could not create notification sound:', e);
    }
}

function connectSSE() {
    if (eventSource) {
        eventSource.close();
    }

    eventSource = new EventSource('/events');

    eventSource.onopen = () => {
        console.log('SSE connected');
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

                if ('Notification' in window && Notification.permission === 'granted') {
                    new Notification('Match Found!', {
                        body: 'Click to accept your match.',
                        icon: '/static/dota-icon.png'
                    });
                }
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

        reconnectTimeout = setTimeout(() => {
            console.log('SSE connection lost, reloading page...');
            window.location.reload();
        }, 2000);
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
