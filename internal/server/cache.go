package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gatekeep/internal/db"
)

const defaultCacheIdle = 5 * time.Minute

var errCacheIdleRange = errors.New("cache idle time must be between 1 second and 24 hours")

type cacheObjectView struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	ExpiresAt string `json:"expiresAt"`
}

func cacheIdle(resource db.Resource) time.Duration {
	if resource.CacheIdleMs != nil && *resource.CacheIdleMs > 0 {
		return time.Duration(*resource.CacheIdleMs) * time.Millisecond
	}
	return defaultCacheIdle
}

func (s *Server) handleListCache(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if !ok {
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	resource, err := s.store.GetResource(r.Context(), id)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	listed := s.caches.List(id, cacheIdle(resource), time.Now())
	objects := make([]cacheObjectView, 0, len(listed))
	for _, object := range listed {
		objects = append(objects, cacheObjectView{
			Name:      object.Name,
			CreatedAt: object.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt: object.UpdatedAt.UTC().Format(time.RFC3339Nano),
			ExpiresAt: object.ExpiresAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": objects})
}

func (s *Server) handleClearCache(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if !ok {
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	if _, err := s.store.GetResource(r.Context(), id); err != nil {
		writeResourceError(w, err)
		return
	}
	name := r.PathValue("name")
	s.caches.Clear(id, name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) watchCaches(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.expireCaches()
		}
	}
}

func (s *Server) expireCaches() {
	if s.store == nil || s.caches == nil {
		return
	}
	now := time.Now()
	for _, id := range s.caches.ResourceIDs() {
		resource, err := s.store.GetResource(context.Background(), id)
		if errors.Is(err, db.ErrNotFound) {
			s.caches.ClearResource(id)
			continue
		}
		if err != nil {
			continue
		}
		s.caches.Expire(id, cacheIdle(resource), now)
	}
}
