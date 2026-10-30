package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/store"
)

// pushServer stands in for a browser vendor's push service: each path answers
// with its own status, and every request's path and Urgency header is kept.
type pushServer struct {
	*httptest.Server
	mu       sync.Mutex
	hits     []string
	urgency  map[string]string
	statuses map[string]int
}

func newPushServer(t *testing.T, statuses map[string]int) *pushServer {
	t.Helper()
	ps := &pushServer{statuses: statuses, urgency: map[string]string{}}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.hits = append(ps.hits, r.URL.Path)
		ps.urgency[r.URL.Path] = r.Header.Get("Urgency")
		ps.mu.Unlock()
		code, ok := statuses[r.URL.Path]
		if !ok {
			code = http.StatusCreated
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *pushServer) paths() []string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := append([]string(nil), ps.hits...)
	slices.Sort(out)
	return out
}

// subscribe saves a subscription with real browser-shaped keys, which the
// sender needs to encrypt the payload.
func subscribe(t *testing.T, st *store.Store, endpoint string) string {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding
	id, err := st.SavePushSubscription(context.Background(), store.PushSubscription{
		Endpoint: endpoint,
		P256dh:   b64.EncodeToString(priv.PublicKey().Bytes()),
		Auth:     b64.EncodeToString(auth),
		Label:    endpoint,
	})
	if err != nil {
		t.Fatalf("save subscription: %v", err)
	}
	return id
}

func newService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hy.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keys, err := LoadKeys(t.TempDir())
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	return New(keys, st, nil), st
}

func TestSendDeliversToEverySubscriptionAndDropsTheOnesThatAreGone(t *testing.T) {
	ps := newPushServer(t, map[string]int{
		"/gone":    http.StatusGone,
		"/missing": http.StatusNotFound,
		"/broken":  http.StatusInternalServerError,
	})
	svc, st := newService(t)
	ctx := context.Background()
	for _, p := range []string{"/a", "/b", "/gone", "/missing", "/broken"} {
		subscribe(t, st, ps.URL+p)
	}

	res, err := svc.Send(ctx, Message{Title: "Done", Body: "Thread finished", ThreadID: "t1"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if want := (Result{Sent: 2, Removed: 2, Failed: 1}); res != want {
		t.Fatalf("result %+v, want %+v", res, want)
	}
	if got, want := ps.paths(), []string{"/a", "/b", "/broken", "/gone", "/missing"}; !slices.Equal(got, want) {
		t.Fatalf("push service saw %v, want %v", got, want)
	}

	left, err := st.ListPushSubscriptions(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var kept []string
	for _, s := range left {
		kept = append(kept, s.Endpoint[len(ps.URL):])
	}
	slices.Sort(kept)
	if want := []string{"/a", "/b", "/broken"}; !slices.Equal(kept, want) {
		t.Fatalf("kept %v, want %v", kept, want)
	}
}

func TestSendWithNoSubscriptionsSendsNothing(t *testing.T) {
	svc, _ := newService(t)
	res, err := svc.Send(context.Background(), Message{Title: "x", Body: "y"})
	if err != nil || res != (Result{}) {
		t.Fatalf("got %+v, %v; want an empty result", res, err)
	}
}

func TestSendForwardsTheMessageUrgencyAndDefaultsToNormal(t *testing.T) {
	ps := newPushServer(t, nil)
	cases := map[string]string{"": "normal", "low": "low", "normal": "normal", "high": "high"}
	for in, want := range cases {
		svc, st := newService(t)
		path := "/u-" + in
		subscribe(t, st, ps.URL+path)
		if _, err := svc.Send(context.Background(), Message{Title: "x", Body: "y", Urgency: in}); err != nil {
			t.Fatalf("send %q: %v", in, err)
		}
		ps.mu.Lock()
		got := ps.urgency[path]
		ps.mu.Unlock()
		if got != want {
			t.Fatalf("urgency %q went out as %q, want %q", in, got, want)
		}
	}
}
