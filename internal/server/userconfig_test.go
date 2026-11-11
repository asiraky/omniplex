package server

import (
	"testing"
	"time"
)

// Whether listings carry update status follows the auto-update setting, so a
// client that just switched it must be told to relist.
func TestSavingUserConfigTellsHarnessSubscribers(t *testing.T) {
	c, _ := commandConn(t)
	id, ch := c.srv.mgr.SubscribeHarnesses()
	defer c.srv.mgr.UnsubscribeHarnesses(id)

	if _, err := run(t, c, "save_user_config", map[string]any{"config": map[string]any{"autoUpdateHarnesses": false}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("saving the user config did not notify harness subscribers")
	}
}
