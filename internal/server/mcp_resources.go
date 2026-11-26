package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gatekeep/internal/db"
	"gatekeep/internal/mcp"
)

func resourceTextURI(id int64) string {
	return "gatekeep://resources/" + strconv.FormatInt(id, 10) + "/context"
}

func (s *Server) handleMCPResources(w http.ResponseWriter, r *http.Request, req rpcRequest, modern bool) {
	result := map[string]any{}
	if modern {
		result["resultType"] = "complete"
	}
	if req.Method == "resources/templates/list" {
		result["resourceTemplates"] = []any{}
		writeRPCResult(w, http.StatusOK, req.ID, result)
		return
	}
	if s.store == nil {
		writeRPCError(w, http.StatusServiceUnavailable, req.ID, mcp.CodeInternal, "database is unavailable", nil)
		return
	}
	if req.Method == "resources/list" {
		var params struct {
			Cursor string `json:"cursor"`
		}
		if len(req.Params) > 0 && json.Unmarshal(req.Params, &params) != nil {
			writeRPCError(w, http.StatusOK, req.ID, mcp.CodeInvalidParams, "invalid resource list params", nil)
			return
		}
		offset := 0
		if params.Cursor != "" {
			var err error
			offset, err = strconv.Atoi(params.Cursor)
			if err != nil || offset < 0 {
				writeRPCError(w, http.StatusOK, req.ID, mcp.CodeInvalidParams, "invalid resource cursor", nil)
				return
			}
		}
		resources, err := s.store.ListResources(r.Context())
		if err != nil {
			writeRPCError(w, http.StatusInternalServerError, req.ID, mcp.CodeInternal, "could not list resources", nil)
			return
		}
		withText := resources[:0]
		for _, resource := range resources {
			if strings.TrimSpace(resource.ResourceText) != "" {
				withText = append(withText, resource)
			}
		}
		resources = withText
		if offset > len(resources) {
			writeRPCError(w, http.StatusOK, req.ID, mcp.CodeInvalidParams, "invalid resource cursor", nil)
			return
		}
		end := offset + min(100, len(resources)-offset)
		listed := make([]map[string]any, 0, end-offset)
		for _, resource := range resources[offset:end] {
			listed = append(listed, map[string]any{
				"uri":         resourceTextURI(resource.ID),
				"name":        resource.Type + "/" + resource.Name,
				"description": "User-provided context for the " + resource.Type + " connection " + resource.Name,
				"mimeType":    "text/plain",
			})
		}
		result["resources"] = listed
		if end < len(resources) {
			result["nextCursor"] = strconv.Itoa(end)
		}
	} else {
		var params struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(req.Params, &params) != nil || params.URI == "" {
			writeRPCError(w, http.StatusOK, req.ID, mcp.CodeInvalidParams, "uri is required", nil)
			return
		}
		rawID := strings.TrimSuffix(strings.TrimPrefix(params.URI, "gatekeep://resources/"), "/context")
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || id <= 0 || resourceTextURI(id) != params.URI {
			writeRPCError(w, http.StatusOK, req.ID, -32002, "resource not found", map[string]any{"uri": params.URI})
			return
		}
		resource, err := s.store.GetResource(r.Context(), id)
		if errors.Is(err, db.ErrNotFound) || (err == nil && strings.TrimSpace(resource.ResourceText) == "") {
			writeRPCError(w, http.StatusOK, req.ID, -32002, "resource not found", map[string]any{"uri": params.URI})
			return
		}
		if err != nil {
			writeRPCError(w, http.StatusInternalServerError, req.ID, mcp.CodeInternal, "could not read resource", nil)
			return
		}
		result["contents"] = []map[string]any{{"uri": params.URI, "mimeType": "text/plain", "text": resource.ResourceText}}
	}
	writeRPCResult(w, http.StatusOK, req.ID, result)
}
