package notify

import "sync"

type note struct {
	title string
	body  string
	url   string
}

var (
	mu      sync.Mutex
	ready   bool
	pending []note
)

// Start prepares notification delivery. Send waits until this finishes.
func Start() {
	start()
}

// Send posts a notification. Calls made before Start are delivered once Start finishes.
func Send(title, body, url string) {
	mu.Lock()
	if !ready {
		pending = append(pending, note{title: title, body: body, url: url})
		mu.Unlock()
		return
	}
	mu.Unlock()
	deliver(title, body, url)
}

func markReady() {
	mu.Lock()
	ready = true
	queued := pending
	pending = nil
	mu.Unlock()
	for _, item := range queued {
		deliver(item.title, item.body, item.url)
	}
}
