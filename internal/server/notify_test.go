package server

import (
	"testing"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/push"
)

// Which transitions are worth interrupting somebody for.
//
// The cases that matter are the negative ones. Notifying on arriving at
// needs_prompt from anywhere would fire on every server restart, every session
// adoption, and every list refresh — and a notification system that cries wolf
// gets its permission revoked by the user within a day.
func TestNotifiable(t *testing.T) {
	cases := []struct {
		name       string
		prev, next string
		prevPhase  string
		want       bool
		urgency    push.Urgency
	}{
		{
			name: "a turn finishing is the whole point",
			prev: projection.AttentionWorking, next: projection.AttentionNeedsPrompt,
			prevPhase: "turn",
			want:      true, urgency: push.UrgencyNormal,
		},
		{
			// A session the user just opened provisions its workspace, which
			// reads as "working", and then goes ready. Telling them their
			// turn is finished while they watch the session open is the
			// notifier crying wolf on the one screen they are looking at.
			name: "a workspace going ready is not a finished turn",
			prev: projection.AttentionWorking, next: projection.AttentionNeedsPrompt,
			prevPhase: "provisioning",
			want:      false,
		},
		{
			name: "neither is a workspace finishing its cleanup",
			prev: projection.AttentionWorking, next: projection.AttentionNeedsPrompt,
			prevPhase: "cleaning",
			want:      false,
		},
		{
			name: "background jobs draining leaves it your turn",
			prev: projection.AttentionBackground, next: projection.AttentionNeedsPrompt,
			want: true, urgency: push.UrgencyNormal,
		},
		{
			name: "a blocked permission is more urgent than a finished turn",
			prev: projection.AttentionWorking, next: projection.AttentionNeedsPermission,
			want: true, urgency: push.UrgencyHigh,
		},
		{
			name: "so is a question",
			prev: projection.AttentionWorking, next: projection.AttentionNeedsAnswer,
			want: true, urgency: push.UrgencyHigh,
		},
		{
			name: "already idle and still idle is not news",
			prev: projection.AttentionNeedsPrompt, next: projection.AttentionNeedsPrompt,
			want: false,
		},
		{
			name: "answering a permission elsewhere must not notify",
			prev: projection.AttentionNeedsPermission, next: projection.AttentionNeedsPrompt,
			want: false,
		},
		{
			name: "starting work is not something to be told about",
			prev: projection.AttentionNeedsPrompt, next: projection.AttentionWorking,
			want: false,
		},
		{
			name: "closing a session is not an interruption",
			prev: projection.AttentionWorking, next: projection.AttentionClosed,
			want: false,
		},
		{
			name: "a failed workspace is shown in the list, not pushed",
			prev: projection.AttentionWorking, next: projection.AttentionFailed,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, urgency, ok := notifiable(tc.prev, tc.next, tc.prevPhase)
			if ok != tc.want {
				t.Fatalf("notifiable(%q, %q) = %v, want %v", tc.prev, tc.next, ok, tc.want)
			}
			if !tc.want {
				return
			}
			if kind == "" {
				t.Error("a notifiable transition produced no kind")
			}
			if urgency != tc.urgency {
				t.Errorf("urgency = %q, want %q", urgency, tc.urgency)
			}
			if bodyFor(kind) == "" {
				t.Errorf("kind %q has no body text, so the notification would be a bare title", kind)
			}
		})
	}
}

// A push endpoint is a bearer credential: anyone holding the URL can send that
// device notifications until it is revoked. It must never reach a log file.
func TestEndpointHostKeepsTheCredentialOutOfLogs(t *testing.T) {
	const endpoint = "https://fcm.googleapis.com/fcm/send/c-SECRET-TOKEN-abc123"

	got := endpointHost(endpoint)

	if got != "fcm.googleapis.com" {
		t.Errorf("endpointHost = %q, want the host alone", got)
	}
	if got == endpoint {
		t.Error("the full endpoint was returned; it would be logged verbatim")
	}
}
