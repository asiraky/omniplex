package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// compressHTTP negotiates gzip for text responses. It deliberately bypasses
// upgrades, ranges and already-encoded bodies. Compression is selected lazily
// on the first write, after handlers such as http.FileServer have supplied a
// Content-Type; binary attachments therefore remain byte-for-byte responses.
func compressHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) ||
			r.Header.Get("Sec-WebSocket-Key") != "" ||
			r.Header.Get("Range") != "" || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		cw := &compressionWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)
		_ = cw.finish()
	})
}

func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if strings.EqualFold(fields[0], "gzip") {
			for _, parameter := range fields[1:] {
				key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
				if ok && strings.EqualFold(key, "q") {
					quality, err := strconv.ParseFloat(value, 64)
					if err == nil && quality <= 0 {
						return false
					}
				}
			}
			return true
		}
	}
	return false
}

type compressionWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	gzip        *gzip.Writer
}

// ResponseController follows Unwrap through middleware to reach deadline and
// full-duplex support on the real server writer.
func (w *compressionWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *compressionWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
}

func (w *compressionWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.start(body)
	}
	if w.gzip != nil {
		return w.gzip.Write(body)
	}
	return w.ResponseWriter.Write(body)
}

func (w *compressionWriter) start(body []byte) {
	w.wroteHeader = true
	if w.Header().Get("Content-Type") == "" && len(body) > 0 {
		w.Header().Set("Content-Type", http.DetectContentType(body))
	}
	if compressibleType(w.Header().Get("Content-Type")) && w.Header().Get("Content-Encoding") == "" && responseHasBody(w.status) {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.gzip = gzip.NewWriter(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *compressionWriter) finish() error {
	if !w.wroteHeader {
		w.start(nil)
	}
	if w.gzip != nil {
		return w.gzip.Close()
	}
	return nil
}

func responseHasBody(status int) bool {
	return status != http.StatusNoContent && status != http.StatusNotModified && (status < 100 || status >= 200)
}

func compressibleType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return strings.HasPrefix(contentType, "text/") ||
		contentType == "application/json" ||
		contentType == "application/javascript" ||
		contentType == "application/manifest+json" ||
		contentType == "image/svg+xml"
}

var _ io.Writer = (*compressionWriter)(nil)
