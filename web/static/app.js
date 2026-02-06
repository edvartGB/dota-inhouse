// SSE connection for real-time updates
let eventSource = null;
let reconnectTimeout = null;

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
        // Parse the HTML and process OOB swaps
        const container = document.createElement('div');
        container.innerHTML = event.data;

        // Find all elements with hx-swap-oob attribute
        container.querySelectorAll('[hx-swap-oob]').forEach(el => {
            const targetId = el.id;
            const target = document.getElementById(targetId);

            if (target) {
                // Replace the target with the new content
                target.replaceWith(el);
                el.removeAttribute('hx-swap-oob');

                // Re-process HTMX attributes on the new content
                if (typeof htmx !== 'undefined') {
                    htmx.process(el);
                }

                // Update queue button visibility if this was a queue update
                if (targetId === 'queue') {
                    updateQueueButtons();
                }
            } else if (el.classList.contains('dialog-overlay')) {
                // Only append dialog overlays to body, not other elements like panels
                document.body.appendChild(el);
                el.removeAttribute('hx-swap-oob');
                if (typeof htmx !== 'undefined') {
                    htmx.process(el);
                }
            }
            // Silently ignore elements that don't have a target (e.g., queue panel on history page)
        });
    };

    eventSource.onerror = (err) => {
        console.error('SSE error:', err);
        eventSource.close();

        // Reconnect after 3 seconds
        reconnectTimeout = setTimeout(() => {
            console.log('Reconnecting SSE...');
            connectSSE();
        }, 3000);
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

// Handle dialog close
document.addEventListener('click', (e) => {
    if (e.target.classList.contains('dialog-overlay')) {
        // Don't allow closing accept dialog by clicking outside
        // Only close if there's a close button
    }
});

// HTMX event handlers for feedback
document.body.addEventListener('htmx:afterRequest', (event) => {
    if (event.detail.failed) {
        console.error('Request failed:', event.detail.xhr.responseText);
        // Could show an error toast here
    }
});
