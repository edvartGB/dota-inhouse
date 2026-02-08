let eventSource = null;
let reconnectTimeout = null;
let audioUnlocked = false;
let reconnectAttempts = 0;

document.addEventListener('DOMContentLoaded', () => {
    connectSSE();

    if ('serviceWorker' in navigator) {
        navigator.serviceWorker.register('/sw.js')
            .then(() => console.log('Service worker registered'));
    }
});


const notificationAudio = new Audio('/static/faceit_trumpet.mp3');
notificationAudio.load();
notificationAudio.volume = 0.7;
notificationAudio.preload = 'auto';


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

    if ('Notification' in window && Notification.permission === 'default') {
        Notification.requestPermission();
    }
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
    if (!('serviceWorker' in navigator)) return;

    navigator.serviceWorker.ready.then(reg => {
        if (reg.active) {
            reg.active.postMessage({ type: 'MATCH_FOUND' });
        }
    });
}
