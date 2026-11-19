package cache

import (
	"testing"
	"time"
)

type stubCloser struct {
	closed bool
}

func (c *stubCloser) Close() error {
	c.closed = true
	return nil
}

func TestUpdateRefreshesAndKeepsCreatedAt(t *testing.T) {
	store := New()
	handle := store.Handle(1, time.Minute)
	first, ok := handle.Update("sequence", func(any, bool) (any, bool) {
		return int64(4), true
	})
	if !ok || first != int64(4) {
		t.Fatalf("first = %v ok = %v", first, ok)
	}
	listed := store.List(1, time.Minute, time.Now())
	if len(listed) != 1 || listed[0].Name != "sequence" {
		t.Fatalf("listed = %+v", listed)
	}
	time.Sleep(2 * time.Millisecond)
	second, ok := handle.Update("sequence", func(current any, ok bool) (any, bool) {
		if !ok || current != int64(4) {
			t.Fatalf("current = %v ok = %v", current, ok)
		}
		return int64(5), true
	})
	if !ok || second != int64(5) {
		t.Fatalf("second = %v ok = %v", second, ok)
	}
	again := store.List(1, time.Minute, time.Now())
	if len(again) != 1 {
		t.Fatalf("again = %+v", again)
	}
	if !again[0].CreatedAt.Equal(listed[0].CreatedAt) || !again[0].UpdatedAt.After(listed[0].UpdatedAt) {
		t.Fatalf("times = %+v then %+v", listed[0], again[0])
	}
	if !again[0].ExpiresAt.Equal(again[0].UpdatedAt.Add(time.Minute)) {
		t.Fatalf("expires = %s updated = %s", again[0].ExpiresAt, again[0].UpdatedAt)
	}
}

func TestIdleDropsAndCloses(t *testing.T) {
	store := New()
	kept := &stubCloser{}
	store.Handle(3, time.Hour).Update("db", func(any, bool) (any, bool) {
		return kept, true
	})
	store.Expire(3, time.Hour, time.Now().Add(2*time.Hour))
	if !kept.closed {
		t.Fatal("idle object was not closed")
	}
	if len(store.ResourceIDs()) != 0 {
		t.Fatalf("ids = %v", store.ResourceIDs())
	}
	value, ok := store.Handle(3, time.Hour).Update("db", func(current any, ok bool) (any, bool) {
		if ok || current != nil {
			t.Fatalf("expired current = %v ok = %v", current, ok)
		}
		return int64(1), true
	})
	if !ok || value != int64(1) {
		t.Fatalf("after idle = %v ok = %v", value, ok)
	}
}

func TestReplaceAndClearCloseTheOldValue(t *testing.T) {
	store := New()
	old := &stubCloser{}
	next := &stubCloser{}
	handle := store.Handle(9, time.Hour)
	handle.Update("db", func(any, bool) (any, bool) { return old, true })
	handle.Update("db", func(any, bool) (any, bool) { return next, true })
	if !old.closed || next.closed {
		t.Fatalf("old closed = %v next closed = %v", old.closed, next.closed)
	}
	handle.Update("db", func(any, bool) (any, bool) { return next, true })
	if next.closed {
		t.Fatal("saving the same value closed it")
	}
	other := &stubCloser{}
	store.Handle(4, time.Hour).Update("db", func(any, bool) (any, bool) { return other, true })
	store.Clear(9, "db")
	if !next.closed || other.closed {
		t.Fatalf("clear closed next = %v other = %v", next.closed, other.closed)
	}
	store.ClearAll()
	if !other.closed || len(store.ResourceIDs()) != 0 {
		t.Fatalf("shutdown closed = %v ids = %v", other.closed, store.ResourceIDs())
	}
}

func TestResourcesStaySeparate(t *testing.T) {
	store := New()
	store.Handle(1, time.Hour).Update("sequence", func(any, bool) (any, bool) { return int64(1), true })
	store.Handle(2, time.Hour).Update("sequence", func(any, bool) (any, bool) { return int64(8), true })
	store.ClearResource(1)
	left := store.List(2, time.Hour, time.Now())
	if len(store.List(1, time.Hour, time.Now())) != 0 || len(left) != 1 {
		t.Fatalf("left = %+v", left)
	}
	got, ok := store.Handle(2, time.Hour).Update("sequence", func(current any, ok bool) (any, bool) {
		return current, false
	})
	if !ok || got != int64(8) {
		t.Fatalf("kept = %v ok = %v", got, ok)
	}
}
