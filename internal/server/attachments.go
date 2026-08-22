package server

import (
	"errors"
	"net/http"
	"os"

	"github.com/asiraky/omniplex/internal/attachment"
)

// handleUploadAttachment takes one image for a session and answers with the id
// the prompt will refer to it by.
//
// The body is the file itself rather than a multipart form: there is exactly
// one part, the browser can hand a File straight to fetch, and it saves
// buffering a copy on the way in. What the client calls it and what it claims
// it is are both ignored — the store sniffs the bytes.
func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	if s.attachments == nil {
		writeError(w, http.StatusNotImplemented, "this server does not store attachments")
		return
	}
	sessionID := r.PathValue("id")
	if _, err := s.store.Session(r.Context(), sessionID); err != nil {
		writeError(w, http.StatusNotFound, "no such session")
		return
	}
	// The store refuses anything past the limit on its own; this stops the
	// server reading a body that was never going to be accepted.
	body := http.MaxBytesReader(w, r.Body, attachment.MaxBytes+1)
	meta, err := s.attachments.Put(sessionID, body)
	switch {
	case errors.Is(err, attachment.ErrUnsupported):
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, attachment.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case err != nil:
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, attachment.ErrTooLarge.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, meta)
	}
}

// handleGetAttachment serves a stored image back. Behind the same gate as
// everything else, which is what lets the UI point an <img> at it: the device
// cookie rides the request without the page having to do anything.
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	if s.attachments == nil {
		http.NotFound(w, r)
		return
	}
	path, mediaType, err := s.attachments.Path(r.PathValue("id"), r.PathValue("attachmentId"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	// The id is a UUID and the bytes under it never change, so a transcript
	// scrolled past and back — or reopened on a phone — refetches nothing.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, path, info.ModTime(), f)
}
