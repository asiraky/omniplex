package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompressHTTPNegotiatesTextOnly(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		body              string
		wantGzip          bool
	}{
		{"json", "application/json", strings.Repeat(`{"tool":"output"}`, 100), true},
		{"javascript", "application/javascript", strings.Repeat("export const value = 1;", 100), true},
		{"image", "image/png", strings.Repeat("not-really-a-png", 100), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := compressHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = io.WriteString(w, tc.body)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("Content-Encoding"); (got == "gzip") != tc.wantGzip {
				t.Fatalf("Content-Encoding = %q, want gzip=%v", got, tc.wantGzip)
			}
			body := rec.Body.String()
			if tc.wantGzip {
				zr, err := gzip.NewReader(rec.Body)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(zr)
				if err != nil {
					t.Fatal(err)
				}
				body = string(decoded)
			}
			if body != tc.body {
				t.Fatalf("body changed: got %d bytes, want %d", len(body), len(tc.body))
			}
		})
	}
}

func TestCompressHTTPSkipsUpgradeAndGzipQZero(t *testing.T) {
	h := compressHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	for _, headers := range []map[string]string{
		{"Accept-Encoding": "gzip;q=0"},
		{"Accept-Encoding": "br, gzip;q=0.0"},
		{"Accept-Encoding": "gzip", "Sec-WebSocket-Key": "upgrade"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Content-Encoding = %q, want empty", got)
		}
	}
}
