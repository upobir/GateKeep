package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gatekeep/internal/db"
	"gatekeep/internal/vault"
)

type resourceInput struct {
	ID                      int64             `json:"id"`
	Name                    string            `json:"name"`
	Type                    string            `json:"type"`
	Config                  map[string]string `json:"config"`
	Secrets                 map[string]string `json:"secrets"`
	ExecutionTimeoutSeconds timeoutField      `json:"executionTimeoutSeconds"`
	CacheIdleSeconds        timeoutField      `json:"cacheIdleSeconds"`
}

// timeoutField is set only when the JSON includes executionTimeoutSeconds.
// Null clears a per-resource limit and the command uses the global timeout.
type timeoutField struct {
	Set   bool
	Value *int
}

func (f *timeoutField) UnmarshalJSON(data []byte) error {
	f.Set = true
	if string(data) == "null" {
		f.Value = nil
		return nil
	}
	var seconds int
	if err := json.Unmarshal(data, &seconds); err != nil {
		return err
	}
	f.Value = &seconds
	return nil
}

type resourceView struct {
	ID                      int64             `json:"id"`
	Name                    string            `json:"name"`
	Type                    string            `json:"type"`
	Config                  map[string]string `json:"config"`
	ResourceText            string            `json:"resourceText"`
	HasSecrets              bool              `json:"hasSecrets"`
	Secrets                 map[string]string `json:"secrets,omitempty"`
	ExecutionTimeoutSeconds *int              `json:"executionTimeoutSeconds"`
	CacheIdleSeconds        *int              `json:"cacheIdleSeconds"`
}

func (s *Server) handleResourceTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.plugins.List())
}

func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	resources, err := s.store.ListResources(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list resources"})
		return
	}
	views := make([]resourceView, 0, len(resources))
	for _, resource := range resources {
		view, err := s.resourceView(resource, false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read a resource"})
			return
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateResource(w http.ResponseWriter, r *http.Request) {
	input, ok := s.readResourceInput(w, r)
	if !ok {
		return
	}
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	if item, ok := s.plugins.Get(input.Type); ok {
		if err := item.Available(); err != nil {
			writeResourceError(w, err)
			return
		}
	}
	configJSON, secrets, err := s.sealResource(input, nil)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	timeout, err := executionTimeoutMs(input.ExecutionTimeoutSeconds, nil)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	idle, err := cacheIdleMs(input.CacheIdleSeconds, nil)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	created, err := s.store.CreateResource(r.Context(), db.Resource{
		Name:               strings.TrimSpace(input.Name),
		Type:               input.Type,
		ConfigJSON:         configJSON,
		SecretsCiphertext:  secrets,
		ExecutionTimeoutMs: timeout,
		CacheIdleMs:        idle,
	})
	if err != nil {
		writeResourceError(w, err)
		return
	}
	view, err := s.resourceView(created, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read a resource"})
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleUpdateResource(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if !ok {
		return
	}
	input, ok := s.readResourceInput(w, r)
	if !ok {
		return
	}
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	existing, err := s.store.GetResource(r.Context(), id)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	if input.Type != existing.Type {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resource type cannot change"})
		return
	}
	configJSON, secrets, err := s.sealResource(input, &existing)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	timeout, err := executionTimeoutMs(input.ExecutionTimeoutSeconds, existing.ExecutionTimeoutMs)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	idle, err := cacheIdleMs(input.CacheIdleSeconds, existing.CacheIdleMs)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	existing.Name = strings.TrimSpace(input.Name)
	existing.ConfigJSON = configJSON
	existing.SecretsCiphertext = secrets
	existing.ExecutionTimeoutMs = timeout
	existing.CacheIdleMs = idle
	if err := s.store.UpdateResource(r.Context(), existing); err != nil {
		writeResourceError(w, err)
		return
	}
	updated, err := s.store.GetResource(r.Context(), id)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	view, err := s.resourceView(updated, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read a resource"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if !ok {
		return
	}
	if s.store == nil || s.vault == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	resource, err := s.store.GetResource(r.Context(), id)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	view, err := s.resourceView(resource, true)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if !ok {
		return
	}
	s.secretsMu.Lock()
	err := s.store.DeleteResource(r.Context(), id)
	s.secretsMu.Unlock()
	if err != nil {
		writeResourceError(w, err)
		return
	}
	s.caches.ClearResource(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTestResource(w http.ResponseWriter, r *http.Request) {
	input, ok := s.readResourceInput(w, r)
	if !ok {
		return
	}
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	var existing *db.Resource
	if input.ID != 0 {
		stored, err := s.store.GetResource(r.Context(), input.ID)
		if err != nil {
			writeResourceError(w, err)
			return
		}
		if input.Type == "" {
			input.Type = stored.Type
		}
		if input.Type != stored.Type {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resource type cannot change"})
			return
		}
		existing = &stored
	}
	config, secrets, err := s.collectFields(input, existing)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	item, _ := s.plugins.Get(input.Type)
	if err := item.Available(); err != nil {
		writeResourceError(w, err)
		return
	}
	if err := item.Test(r.Context(), config, secrets); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) readResourceInput(w http.ResponseWriter, r *http.Request) (resourceInput, bool) {
	if s.store == nil || s.vault == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return resourceInput{}, false
	}
	var input resourceInput
	if !readJSONLimit(w, r, &input, 1<<16) {
		return resourceInput{}, false
	}
	input.Name = strings.TrimSpace(input.Name)
	return input, true
}

func (s *Server) sealResource(input resourceInput, existing *db.Resource) (string, []byte, error) {
	if input.Name == "" {
		return "", nil, errors.New("name is required")
	}
	config, secrets, err := s.collectFields(input, existing)
	if err != nil {
		return "", nil, err
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", nil, err
	}
	plain, err := json.Marshal(secrets)
	if err != nil {
		return "", nil, err
	}
	sealed, err := s.vault.Seal(plain)
	if err != nil {
		return "", nil, err
	}
	return string(configJSON), sealed, nil
}

func (s *Server) collectFields(input resourceInput, existing *db.Resource) (map[string]string, map[string]string, error) {
	item, ok := s.plugins.Get(input.Type)
	if !ok {
		return nil, nil, fmt.Errorf("unknown resource type %q", input.Type)
	}
	savedSecrets := map[string]string{}
	if existing != nil && len(existing.SecretsCiphertext) > 0 {
		plain, err := s.vault.Open(existing.SecretsCiphertext)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(plain, &savedSecrets); err != nil {
			return nil, nil, err
		}
	}
	config := map[string]string{}
	secrets := map[string]string{}
	knownConfig := map[string]bool{}
	knownSecrets := map[string]bool{}
	for _, field := range item.Info().Fields {
		if field.Secret {
			knownSecrets[field.Name] = true
			value := input.Secrets[field.Name]
			if value == "" {
				value = savedSecrets[field.Name]
			}
			if value == "" {
				if field.Optional {
					continue
				}
				return nil, nil, fmt.Errorf("%s is required", field.Label)
			}
			secrets[field.Name] = value
			continue
		}
		knownConfig[field.Name] = true
		value := strings.TrimSpace(input.Config[field.Name])
		if value == "" {
			if field.Optional {
				continue
			}
			return nil, nil, fmt.Errorf("%s is required", field.Label)
		}
		config[field.Name] = value
	}
	for name := range input.Config {
		if !knownConfig[name] {
			return nil, nil, fmt.Errorf("unknown config field %q", name)
		}
	}
	for name := range input.Secrets {
		if !knownSecrets[name] {
			return nil, nil, fmt.Errorf("unknown secret field %q", name)
		}
	}
	return config, secrets, nil
}

func (s *Server) resourceView(resource db.Resource, includeSecrets bool) (resourceView, error) {
	config := map[string]string{}
	if err := json.Unmarshal([]byte(resource.ConfigJSON), &config); err != nil {
		return resourceView{}, err
	}
	view := resourceView{
		ID:                      resource.ID,
		Name:                    resource.Name,
		Type:                    resource.Type,
		Config:                  config,
		ResourceText:            resource.ResourceText,
		HasSecrets:              len(resource.SecretsCiphertext) > 0,
		ExecutionTimeoutSeconds: timeoutSeconds(resource.ExecutionTimeoutMs),
		CacheIdleSeconds:        timeoutSeconds(resource.CacheIdleMs),
	}
	if includeSecrets && len(resource.SecretsCiphertext) > 0 {
		plain, err := s.vault.Open(resource.SecretsCiphertext)
		if err != nil {
			return resourceView{}, err
		}
		secrets := map[string]string{}
		if err := json.Unmarshal(plain, &secrets); err != nil {
			return resourceView{}, err
		}
		view.Secrets = secrets
	}
	return view, nil
}

func executionTimeoutMs(field timeoutField, existing *int64) (*int64, error) {
	return optionalSecondsMs(field, existing, errExecutionRange)
}

func cacheIdleMs(field timeoutField, existing *int64) (*int64, error) {
	return optionalSecondsMs(field, existing, errCacheIdleRange)
}

func optionalSecondsMs(field timeoutField, existing *int64, invalid error) (*int64, error) {
	if !field.Set {
		return existing, nil
	}
	if field.Value == nil {
		return nil, nil
	}
	seconds := *field.Value
	if seconds < 1 || seconds > int(maxApprovalTimeout/time.Second) {
		return nil, invalid
	}
	ms := int64(seconds) * 1000
	return &ms, nil
}

func timeoutSeconds(ms *int64) *int {
	if ms == nil || *ms <= 0 {
		return nil
	}
	seconds := int(*ms / 1000)
	return &seconds
}

func resourceID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid resource id"})
		return 0, false
	}
	return id, true
}

func writeResourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "resource not found"})
	case errors.Is(err, vault.ErrLocked):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "locked"})
	case err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed"):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a resource with that type and name already exists"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func (s *Server) handleUpdateResourceText(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if !ok {
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	var input struct {
		Text *string `json:"text"`
	}
	if !readJSONLimit(w, r, &input, 1<<20) {
		return
	}
	if input.Text == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	if err := s.store.UpdateResourceText(r.Context(), id, *input.Text); err != nil {
		writeResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": *input.Text})
}
