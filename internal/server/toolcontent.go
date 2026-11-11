package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/asiraky/omniplex/internal/store"
)

// handleToolContent serves one tool call's full output. The timeline carries
// only a preview of each, so the whole of a long one is fetched when the
// reader opens it. Read straight from the log: a thread's actor is never
// started or copied for it, which on a long thread is the expensive part.
func (s *Server) handleToolContent(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	threadID, itemID := r.PathValue("id"), r.PathValue("itemId")
	content, err := s.store.ToolContent(r.Context(), threadID, itemID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such tool call")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
	s.logf("tool_content thread=%s item=%s duration_ms=%d entries=%d bytes=%d",
		threadID, itemID, time.Since(started).Milliseconds(), len(content), len(payload))
}
