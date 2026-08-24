package server

import "testing"

// The ledger is what stops a retried mutation running twice, so the exemption
// list is the one place a mistake would be silent and expensive. This pins the
// property that matters: nothing that changes anything is ever exempt.
func TestOnlyPolledReadsSkipTheCommandLedger(t *testing.T) {
	if !pollingCommand("session_pr") {
		t.Fatal("the polled pull-request lookup writes a ledger row every two minutes")
	}
	for _, mutation := range []string{
		"prompt", "delete_session", "force_delete_session", "cleanup_session",
		"close_session", "create_session", "save_project", "save_user_config",
		"resolve_permission", "cancel",
		"create_label", "save_label", "delete_label", "set_session_label",
		"add_project", "delete_project",
	} {
		if pollingCommand(mutation) {
			t.Fatalf("%q changes something and must keep its replay guarantee", mutation)
		}
	}
}
