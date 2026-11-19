package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type healthStatus struct {
	Server   string `json:"server"`
	Database string `json:"database"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(s.healthJSON())
}

func (s *Server) healthJSON() []byte {
	body, err := json.Marshal(s.health())
	if err != nil {
		return []byte(`{"server":"up","database":"down"}`)
	}
	return body
}

func (s *Server) health() healthStatus {
	status := healthStatus{Server: "up", Database: "down"}
	if s.store != nil && s.store.Ping() == nil {
		status.Database = "up"
	}
	return status
}

func (s *Server) watchHealth(ctx context.Context) {
	last := string(s.healthJSON())
	s.hub.Publish("status", last)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			body := string(s.healthJSON())
			if body == last {
				continue
			}
			last = body
			s.hub.Publish("status", body)
		}
	}
}
