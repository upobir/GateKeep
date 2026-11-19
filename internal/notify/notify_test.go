package notify

import "testing"

func TestNotificationsCanBeTurnedOff(t *testing.T) {
	t.Cleanup(func() { SetEnabled(true) })
	if !Enabled() {
		t.Fatal("notifications start on")
	}
	SetEnabled(false)
	if Enabled() {
		t.Fatal("notifications stayed on")
	}
	Send("Approval needed", "waiting", "http://127.0.0.1:8765")
	mu.Lock()
	queued := len(pending)
	mu.Unlock()
	if queued != 0 {
		t.Fatalf("queued %d notifications while off", queued)
	}
}
