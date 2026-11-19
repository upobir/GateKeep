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
	enabled = true
	pending []note
)

// Start prepares notification delivery. Send waits until this finishes.
func Start() {
	start()
}

// Enabled reports whether new approval requests post a notification.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// SetEnabled turns notification delivery on or off.
func SetEnabled(on bool) {
	mu.Lock()
	enabled = on
	if !on {
		pending = nil
	}
	mu.Unlock()
}

// Send posts a notification. Calls made before Start are delivered once Start finishes.
func Send(title, body, url string) {
	mu.Lock()
	if !enabled {
		mu.Unlock()
		return
	}
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
	if !enabled {
		pending = nil
		mu.Unlock()
		return
	}
	queued := pending
	pending = nil
	mu.Unlock()
	for _, item := range queued {
		deliver(item.title, item.body, item.url)
	}
}
