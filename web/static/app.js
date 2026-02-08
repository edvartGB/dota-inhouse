// SSE connection for real-time updates
let eventSource = null;
let reconnectTimeout = null;
let previousQueueSize = 0;
let audioUnlocked = false;

// Unlock audio after first user interaction (required by browsers)
document.addEventListener('click', () => {
    if (!audioUnlocked) {
        const audio = new Audio('/static/faceit_trumpet.mp3');
        audio.play()
            .then(() => {
                audio.pause();
                audio.currentTime = 0;
                audioUnlocked = true;
                console.log("Audio unlocked");
            })
            .catch(() => {});
    }
}, { once: true });

// Play notification sound
function playNotificationSound() {
    if (!audioUnlocked) {
        console.warn("Audio not unlocked yet");
        return;
    }

    try {
        const audio = new Audio('/static/faceit_trumpet.mp3');
        audio.volume = 0.7;
        audio.play().catch(e =>
            console.warn('Could not play notification sound:', e)
        );
    } catch (e) {
        console.warn('Could not play notification sound:', e);
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

            if (target) {
                target.replaceWith(el);
                el.removeAttribute('hx-swap-oob');

                if (typeof htmx !== 'undefined') {
                    htmx.process(el);
                }

                if (targetId === 'queue') {
                    const queuePlayers = el.querySelectorAll('.player:not(.empty)');
                    const currentQueueSize = queuePlayers.length;
                    const maxPlayers = parseInt(el.dataset.maxPlayers || '10');

                    console.log("Queue size:", currentQueueSize, "/", maxPlayers);

                    const inQueue = el.dataset.inQueue === 'true';

                    if (inQueue && currentQueueSize === maxPlayers && previousQueueSize < maxPlayers) {
                        console.log("Queue became full");

                        playNotificationSound();

                        if ('Notification' in window) {
                            console.log("Notification permission:", Notification.permission);
                        }

                        if ('Notification' in window && Notification.permission === 'granted') {
                            new Notification('Match Ready!', {
                                body: 'Queue is full, match starting...',
                                icon: '/static/dota-icon.png'
                            });
                        }
                    }

                    previousQueueSize = currentQueueSize;
                }
            }
            else if (el.classList.contains('dialog-overlay') || el.querySelector('.dialog-overlay')) {
                document.body.appendChild(el);
                el.removeAttribute('hx-swap-oob');

                if (typeof htmx !== 'undefined') {
                    htmx.process(el);
                }

                console.log("Dialog overlay received");

                playNotificationSound();

                if ('Notification' in window && Notification.permission === 'granted') {
                    new Notification('Match Found!', {
                        body: 'Click to accept your match.',
                        icon: '/static/dota-icon.png'
                    });
                }
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

    if ('Notification' in window) {
        console.log("Notification permission:", Notification.permission);
    }

    if ('Notification' in window && Notification.permission === 'default') {
        Notification.requestPermission();
    }
});

// Cleanup on page unload
window.addEventListener('beforeunload', () => {
    if (eventSource) {
        eventSource.close();
    }
});

