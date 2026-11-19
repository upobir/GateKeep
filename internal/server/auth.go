package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gatekeep/internal/vault"
)

const sessionCookie = "gatekeep_session"

func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.vault == nil || !s.vault.Valid(sessionToken(r)) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "locked"})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	status, err := s.vault.Status(r.Context(), sessionToken(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read lock state"})
		return
	}
	body := map[string]any{
		"state":                 status.State,
		"unlockDurationSeconds": status.UnlockDurationSeconds,
	}
	if status.State == "unlocked" {
		body["expiresAt"] = status.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	var body struct {
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	session, err := s.vault.Setup(r.Context(), body.Password, body.Confirm)
	if err != nil {
		writeVaultError(w, err)
		return
	}
	setSessionCookie(w, session)
	writeJSON(w, http.StatusOK, map[string]string{"state": "unlocked"})
}

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	session, err := s.vault.Unlock(r.Context(), body.Password)
	if err != nil {
		writeVaultError(w, err)
		return
	}
	setSessionCookie(w, session)
	writeJSON(w, http.StatusOK, map[string]string{"state": "unlocked"})
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	if s.vault != nil {
		s.vault.Lock()
	}
	s.hub.Publish("auth", `{"state":"locked"}`)
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"state": "locked"})
}

func (s *Server) handleUnlockDuration(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	var body struct {
		Seconds int `json:"seconds"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	err := s.vault.SetUnlockDuration(r.Context(), time.Duration(body.Seconds)*time.Second)
	if errors.Is(err, vault.ErrUnlockDuration) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save unlock duration"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unlockDurationSeconds": body.Seconds})
}

func (s *Server) watchSession(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.vault != nil && s.vault.Expire() {
				s.hub.Publish("auth", `{"state":"locked"}`)
			}
		}
	}
}

func writeVaultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, vault.ErrPasswordShort), errors.Is(err, vault.ErrPasswordMatch), errors.Is(err, vault.ErrNotSetup):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, vault.ErrWrongPassword):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
	case errors.Is(err, vault.ErrAlreadySetup):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not unlock"})
	}
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func setSessionCookie(w http.ResponseWriter, session vault.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	return readJSONLimit(w, r, dest, 4096)
}

func readJSONLimit(w http.ResponseWriter, r *http.Request, dest any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
