package tts

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUpstreamErrorTellsQuotaFromABadKey(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		code     string
		quota    bool
		rejected bool
	}{
		{"out of credit", http.StatusUnauthorized, `{"detail":{"status":"quota_exceeded","message":"This request exceeds your quota."}}`, "quota_exceeded", true, false},
		{"payment required", http.StatusPaymentRequired, `{"detail":"quota exceeded"}`, "", true, false},
		{"bad key", http.StatusUnauthorized, `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`, "invalid_api_key", false, true},
		{"no permission", http.StatusForbidden, `{"detail":{"status":"missing_permissions"}}`, "missing_permissions", false, true},
		{"rate limited", http.StatusTooManyRequests, `{"detail":{"status":"too_many_concurrent_requests","message":"Slow down"}}`, "too_many_concurrent_requests", false, false},
		{"validation list", http.StatusUnprocessableEntity, `{"detail":[{"msg":"field required"}]}`, "", false, false},
		{"not json", http.StatusBadGateway, `<html>`, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := upstreamError(&http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))})
			if up.Code != tc.code {
				t.Errorf("Code = %q, want %q", up.Code, tc.code)
			}
			if up.Quota() != tc.quota {
				t.Errorf("Quota() = %v, want %v", up.Quota(), tc.quota)
			}
			if up.Rejected() != tc.rejected {
				t.Errorf("Rejected() = %v, want %v", up.Rejected(), tc.rejected)
			}
		})
	}
}
