package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/push"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

func newPushService(t *testing.T, st *store.Store) *push.Service {
	t.Helper()
	keys, err := push.LoadKeys(t.TempDir())
	if err != nil {
		t.Fatalf("push keys: %v", err)
	}
	return push.New(keys, st, nil)
}

// pushed is one notification as the device it went to reads it.
type pushed struct {
	Title, Body, ThreadID, Urgency string
}

// pushInbox stands in for a browser vendor's push service and the devices
// behind it. Each subscription it hands out has real device keys, so what
// arrives is decrypted the way the browser would.
type pushInbox struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	// devices maps an endpoint path to its private key and auth secret.
	devices map[string]inboxDevice
	got     []pushed
	gone    map[string]bool
}

type inboxDevice struct {
	key  *ecdh.PrivateKey
	auth []byte
}

func newPushInbox(t *testing.T) *pushInbox {
	t.Helper()
	in := &pushInbox{t: t, devices: map[string]inboxDevice{}, gone: map[string]bool{}}
	in.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		in.mu.Lock()
		defer in.mu.Unlock()
		if in.gone[r.URL.Path] {
			w.WriteHeader(http.StatusGone)
			return
		}
		dev, ok := in.devices[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var p struct{ Title, Body, ThreadID, Urgency string }
		if err := json.Unmarshal(decryptPush(t, dev, body), &p); err != nil {
			t.Errorf("payload: %v", err)
		}
		in.got = append(in.got, pushed{p.Title, p.Body, p.ThreadID, p.Urgency})
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(in.srv.Close)
	return in
}

// subscribe makes a device and saves its subscription, as the subscribe
// endpoint would.
func (in *pushInbox) subscribe(st *store.Store, name string) string {
	in.t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		in.t.Fatal(err)
	}
	secret := make([]byte, 16)
	rand.Read(secret)
	in.mu.Lock()
	in.devices["/"+name] = inboxDevice{key: key, auth: secret}
	in.mu.Unlock()
	b64 := base64.RawURLEncoding
	id, err := st.SavePushSubscription(context.Background(), store.PushSubscription{
		Endpoint: in.srv.URL + "/" + name,
		P256dh:   b64.EncodeToString(key.PublicKey().Bytes()),
		Auth:     b64.EncodeToString(secret),
		Label:    name,
	})
	if err != nil {
		in.t.Fatal(err)
	}
	return id
}

func (in *pushInbox) unsubscribeAtVendor(name string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.gone["/"+name] = true
}

// take returns what arrived since the last take.
func (in *pushInbox) take() []pushed {
	in.mu.Lock()
	defer in.mu.Unlock()
	out := in.got
	in.got = nil
	return out
}

// decryptPush opens an aes128gcm Web Push body (RFC 8291 and RFC 8188) with
// the device's keys.
func decryptPush(t *testing.T, dev inboxDevice, body []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("push body too short: %d bytes", len(body))
	}
	salt := body[:16]
	idLen := int(body[20])
	senderPub := body[21 : 21+idLen]
	ciphertext := body[21+idLen:]
	_ = binary.BigEndian.Uint32(body[16:20]) // record size; one record here

	sender, err := ecdh.P256().NewPublicKey(senderPub)
	if err != nil {
		t.Fatalf("sender key: %v", err)
	}
	shared, err := dev.key.ECDH(sender)
	if err != nil {
		t.Fatal(err)
	}
	info := append([]byte("WebPush: info\x00"), dev.key.PublicKey().Bytes()...)
	info = append(info, senderPub...)
	ikm, err := hkdf.Key(sha256.New, shared, dev.auth, string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	// Padding is zeros after a 0x02 delimiter on the last record.
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		t.Fatalf("no record delimiter in %q", plain)
	}
	return plain[:len(plain)-1]
}

// pushServer is a server with push on, reached over loopback.
func pushServer(t *testing.T) (*httptest.Server, *store.Store, *push.Service, *auth.Guard) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := thread.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)
	svc := newPushService(t, st)
	guard := auth.New(st, true, auth.DefaultPort)
	srv := New(Options{Manager: mgr, Store: st, Guard: guard, Push: svc})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, svc, guard
}

// doJSON sends v as JSON and decodes the reply into a map.
func doJSON(t *testing.T, client *http.Client, method, url string, v any) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, body)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func subscription(endpoint, label string) map[string]any {
	return map[string]any{
		"endpoint": endpoint,
		"keys":     map[string]string{"p256dh": "BPub", "auth": "secret"},
		"label":    label,
	}
}

// ---- HTTP ----

func TestASubscriptionCanBeSavedListedRefreshedByResubscribingAndRemoved(t *testing.T) {
	ts, _, svc, _ := pushServer(t)
	c := http.DefaultClient
	endpoint := "https://push.example/sub/abc"

	code, out := doJSON(t, c, http.MethodPost, ts.URL+"/api/push/subscriptions", subscription(endpoint, "Pixel"))
	if code != http.StatusOK || out["id"] == "" {
		t.Fatalf("subscribe: %d %v", code, out)
	}
	id := out["id"]

	// The same browser subscribing again keeps one row, with the new label.
	code, out = doJSON(t, c, http.MethodPost, ts.URL+"/api/push/subscriptions", subscription(endpoint, "Pixel 9"))
	if code != http.StatusOK || out["id"] != id {
		t.Fatalf("resubscribe gave %d %v, want the same id %v", code, out, id)
	}

	res, err := c.Get(ts.URL + "/api/push")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var list struct {
		PublicKey     string `json:"publicKey"`
		Subscriptions []map[string]any
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if list.PublicKey != svc.PublicKey() || len(list.Subscriptions) != 1 {
		t.Fatalf("list %s", raw)
	}
	sub := list.Subscriptions[0]
	if sub["id"] != id || sub["label"] != "Pixel 9" || sub["endpoint"] != endpoint || sub["deviceId"] != "" {
		t.Fatalf("listed %v", sub)
	}
	if strings.Contains(string(raw), "BPub") || strings.Contains(string(raw), "secret") {
		t.Fatalf("subscription keys went back to the client: %s", raw)
	}

	code, out = doJSON(t, c, http.MethodDelete, ts.URL+"/api/push/subscriptions/"+id.(string), nil)
	if code != http.StatusOK || out["removed"] != id {
		t.Fatalf("delete: %d %v", code, out)
	}
	code, out = doJSON(t, c, http.MethodDelete, ts.URL+"/api/push/subscriptions/"+id.(string), nil)
	if code != http.StatusNotFound || out["error"] == "" {
		t.Fatalf("deleting it again: %d %v, want 404", code, out)
	}
	_, out = doJSON(t, c, http.MethodGet, ts.URL+"/api/push", nil)
	if subs, _ := out["subscriptions"].([]any); len(subs) != 0 {
		t.Fatalf("still listed after delete: %v", out)
	}
}

func TestASubscriptionFromAPairedDeviceRecordsThatDevice(t *testing.T) {
	ts, st, _, guard := pushServer(t)
	remote := httptest.NewServer(asRemote(ts.Config.Handler))
	t.Cleanup(remote.Close)
	code, err := guard.NewPairingCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if status, out := doJSON(t, client, http.MethodPost, remote.URL+"/api/pair", map[string]string{"code": auth.FormatCode(code), "label": "phone"}); status != http.StatusOK {
		t.Fatalf("pair: %d %v", status, out)
	}

	if status, out := doJSON(t, client, http.MethodPost, remote.URL+"/api/push/subscriptions", subscription("https://push.example/p", "phone")); status != http.StatusOK {
		t.Fatalf("subscribe: %d %v", status, out)
	}
	devices, err := st.ListDevices(context.Background())
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices %v %v", devices, err)
	}
	subs, err := st.ListPushSubscriptions(context.Background())
	if err != nil || len(subs) != 1 || subs[0].DeviceID != devices[0].ID {
		t.Fatalf("subscription %+v, want device %s", subs, devices[0].ID)
	}
}

func TestSubscribeRefusesASubscriptionMissingItsEndpointOrKeys(t *testing.T) {
	ts, st, _, _ := pushServer(t)
	cases := map[string]any{
		"no endpoint":    map[string]any{"keys": map[string]string{"p256dh": "a", "auth": "b"}},
		"http endpoint":  subscription("http://push.example/x", ""),
		"not a URL":      subscription("push.example", ""),
		"no keys":        map[string]any{"endpoint": "https://push.example/x"},
		"no auth secret": map[string]any{"endpoint": "https://push.example/x", "keys": map[string]string{"p256dh": "a"}},
		"not an object":  []string{"x"},
	}
	for name, body := range cases {
		code, out := doJSON(t, http.DefaultClient, http.MethodPost, ts.URL+"/api/push/subscriptions", body)
		if code != http.StatusBadRequest || out["error"] == "" {
			t.Errorf("%s: %d %v, want 400 with an error", name, code, out)
		}
	}
	if subs, _ := st.ListPushSubscriptions(context.Background()); len(subs) != 0 {
		t.Fatalf("a bad subscription was saved: %+v", subs)
	}
}

func TestTestPushGoesToEveryDeviceAndDropsTheOnesThatAreGone(t *testing.T) {
	ts, st, _, _ := pushServer(t)
	inbox := newPushInbox(t)
	inbox.subscribe(st, "phone")
	inbox.subscribe(st, "laptop")
	inbox.subscribe(st, "old")
	inbox.unsubscribeAtVendor("old")

	code, out := doJSON(t, http.DefaultClient, http.MethodPost, ts.URL+"/api/push/test", nil)
	if code != http.StatusOK || out["sent"] != 2.0 || out["removed"] != 1.0 || out["failed"] != 0.0 {
		t.Fatalf("test push: %d %v", code, out)
	}
	got := inbox.take()
	if len(got) != 2 {
		t.Fatalf("devices got %+v", got)
	}
	for _, p := range got {
		if p.Title != "Omniplex" || p.Body != "Test notification" || p.ThreadID != "" {
			t.Errorf("test push arrived as %+v", p)
		}
	}
	if subs, _ := st.ListPushSubscriptions(context.Background()); len(subs) != 2 {
		t.Fatalf("subscriptions after: %+v", subs)
	}
}

func TestPushRoutesSayPushIsOffOnAServerWithoutIt(t *testing.T) {
	h, _ := testServer(t)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/push"},
		{http.MethodPost, "/api/push/subscriptions"},
		{http.MethodDelete, "/api/push/subscriptions/x"},
		{http.MethodPost, "/api/push/test"},
	} {
		code, out := doJSON(t, http.DefaultClient, r.method, ts.URL+r.path, subscription("https://push.example/x", ""))
		if code != http.StatusNotImplemented || out["error"] == "" {
			t.Errorf("%s %s: %d %v, want 501 with an error", r.method, r.path, code, out)
		}
	}
}

func TestTheServiceWorkerScriptIsServedToAnUnpairedBrowserWhileThePushAPIIsNot(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := thread.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)
	bundle := testBundle("index-aaaa1111.js", "app")
	bundle["sw.js"] = &fstest.MapFile{Data: []byte("self.addEventListener('push', () => {})")}
	srv := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort), WebFS: bundle, Push: newPushService(t, st)})
	ts := httptest.NewServer(asRemote(srv.Handler()))
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "addEventListener('push'") {
		t.Fatalf("GET /sw.js unpaired: %d %q", res.StatusCode, body)
	}

	res, err = http.Get(ts.URL + "/api/push")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/push unpaired: %d, want 401", res.StatusCode)
	}
}

// Without a service worker in the bundle, its public path must not hand an
// unpaired browser the app's document instead.
func TestAMissingServiceWorkerScriptIsNotFoundRatherThanTheApp(t *testing.T) {
	res := serve(t, newTestAssets(t, "index-aaaa1111.js", "app"), "/sw.js", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /sw.js with none built: %d, want 404", res.StatusCode)
	}
}

// ---- notify ----

// presence reports what the client shows, and waits until the server has
// read it: frames are handled in order, so the pong comes after.
func (c *wsClient) presence(visible bool, threadID string) {
	c.t.Helper()
	c.write(clientFrame{Type: "presence", Visible: visible, ThreadID: threadID})
	c.write(clientFrame{Type: "ping"})
	for {
		if f := c.read(); f.Type == "pong" {
			return
		}
	}
}

func TestNotifySendsTheMessageWithTheThreadTitleToEverySubscribedDevice(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	if err := r.st.SetTitle(context.Background(), threadID, "Fix the login bug"); err != nil {
		t.Fatal(err)
	}
	inbox := newPushInbox(t)
	inbox.subscribe(r.st, "phone")
	inbox.subscribe(r.st, "laptop")

	got := r.call(threadID, "notify", map[string]string{"title": "Approve the migration?", "body": "It drops the old table.", "urgency": "high"})
	if got.code != http.StatusOK || got.text != "Sent to 2 devices." {
		t.Fatalf("notify: %+v", got)
	}
	arrived := inbox.take()
	if len(arrived) != 2 {
		t.Fatalf("devices got %+v", arrived)
	}
	want := pushed{Title: "Approve the migration?", Body: "It drops the old table.\nFix the login bug", ThreadID: threadID, Urgency: "high"}
	for _, p := range arrived {
		if p != want {
			t.Errorf("arrived as %+v, want %+v", p, want)
		}
	}
}

func TestNotifyOnAnUntitledThreadSendsTheBodyAloneAtNormalUrgency(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	inbox := newPushInbox(t)
	inbox.subscribe(r.st, "phone")

	got := r.call(threadID, "notify", map[string]string{"title": "Done", "body": "The benchmark run finished."})
	if got.code != http.StatusOK || got.text != "Sent to 1 device." {
		t.Fatalf("notify: %+v", got)
	}
	arrived := inbox.take()
	if len(arrived) != 1 || arrived[0].Body != "The benchmark run finished." || arrived[0].Urgency != "normal" {
		t.Fatalf("arrived as %+v", arrived)
	}
}

func TestNotifyIsSkippedWhileTheThreadIsOnAVisiblePageAndSentOnceThePageIsHidden(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	inbox := newPushInbox(t)
	inbox.subscribe(r.st, "phone")
	args := map[string]string{"title": "Blocked", "body": "Which database?"}

	r.ws.presence(true, threadID)
	got := r.call(threadID, "notify", args)
	if got.code != http.StatusOK || !strings.Contains(got.text, "Not sent") || !strings.Contains(got.text, "open") {
		t.Fatalf("watched notify: %+v", got)
	}
	if arrived := inbox.take(); len(arrived) != 0 {
		t.Fatalf("pushed while the thread was on screen: %+v", arrived)
	}

	r.ws.presence(false, threadID)
	got = r.call(threadID, "notify", args)
	if got.code != http.StatusOK || got.text != "Sent to 1 device." || len(inbox.take()) != 1 {
		t.Fatalf("notify with the page hidden: %+v", got)
	}
}

func TestPresenceFollowsTheClientToTheThreadItSwitchedTo(t *testing.T) {
	r := newToolRig(t)
	a := r.thread(r.ctl.id, "default")
	b := r.thread(r.ctl.id, "default")
	inbox := newPushInbox(t)
	inbox.subscribe(r.st, "phone")
	args := map[string]string{"title": "Blocked", "body": "Which database?"}

	r.ws.presence(true, a)
	if got := r.call(b, "notify", args); got.text != "Sent to 1 device." {
		t.Fatalf("notify from a thread not on screen: %+v", got)
	}
	r.ws.presence(true, b)
	if got := r.call(a, "notify", args); got.text != "Sent to 1 device." {
		t.Fatalf("notify from the thread the client left: %+v", got)
	}
	if got := r.call(b, "notify", args); !strings.Contains(got.text, "Not sent") {
		t.Fatalf("notify from the thread the client switched to: %+v", got)
	}
	if arrived := inbox.take(); len(arrived) != 2 {
		t.Fatalf("devices got %+v", arrived)
	}
}

func TestPresenceIsDroppedWhenTheConnectionCloses(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	inbox := newPushInbox(t)
	inbox.subscribe(r.st, "phone")

	phone := dialWS(t, r.srv.Handler())
	phone.presence(true, threadID)
	if !r.srv.watched(threadID) {
		t.Fatal("thread not watched after the client reported it on screen")
	}
	phone.conn.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for r.srv.watched(threadID) {
		if time.Now().After(deadline) {
			t.Fatal("thread still watched after its only viewer disconnected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := r.call(threadID, "notify", map[string]string{"title": "Done", "body": "Finished."})
	if got.text != "Sent to 1 device." || len(inbox.take()) != 1 {
		t.Fatalf("notify after the viewer left: %+v", got)
	}
}

func TestNotifyTellsTheAgentWhenNoDeviceHasNotificationsOn(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	got := r.call(threadID, "notify", map[string]string{"title": "Done", "body": "Finished."})
	if got.code != http.StatusOK || !strings.Contains(got.text, "Not sent") || !strings.Contains(got.text, "Settings") {
		t.Fatalf("notify with no subscriptions: %+v", got)
	}
}

func TestNotifyReportsExpiredSubscriptionsAndFailsWhenNoDeviceCouldBeReached(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	inbox := newPushInbox(t)
	inbox.subscribe(r.st, "phone")
	inbox.subscribe(r.st, "old")
	inbox.unsubscribeAtVendor("old")
	args := map[string]string{"title": "Done", "body": "Finished."}

	got := r.call(threadID, "notify", args)
	if got.code != http.StatusOK || !strings.HasPrefix(got.text, "Sent to 1 device.") || !strings.Contains(got.text, "expired") {
		t.Fatalf("notify with one expired subscription: %+v", got)
	}

	// A push service that is down is a failure the agent must see as one.
	inbox.srv.Close()
	got = r.call(threadID, "notify", args)
	if got.code != http.StatusBadGateway || !strings.Contains(got.err, "Failed on 1 device") {
		t.Fatalf("notify with the push service down: %+v", got)
	}
}

func TestNotifyRefusesAMissingTitleOrBodyAndAnUnknownUrgency(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	inbox := newPushInbox(t)
	inbox.subscribe(r.st, "phone")
	for name, args := range map[string]map[string]string{
		"no title":    {"body": "Finished."},
		"blank title": {"title": "  ", "body": "Finished."},
		"no body":     {"title": "Done"},
		"bad urgency": {"title": "Done", "body": "Finished.", "urgency": "urgent"},
		"long title":  {"title": strings.Repeat("x", maxNotifyTitle+1), "body": "Finished."},
		"long body":   {"title": "Done", "body": strings.Repeat("x", maxNotifyBody+1)},
	} {
		got := r.call(threadID, "notify", args)
		if got.code != http.StatusBadRequest || got.err == "" {
			t.Errorf("%s: %+v, want 400 with an error", name, got)
		}
	}
	if arrived := inbox.take(); len(arrived) != 0 {
		t.Fatalf("a refused notify was sent: %+v", arrived)
	}
}

func TestNotifySaysPushIsNotSetUpOnAServerWithoutIt(t *testing.T) {
	_, err := (&Server{}).notify(context.Background(), toolThread{id: "t1"}, []byte(`{"title":"Done","body":"Finished."}`))
	te, ok := err.(*toolError)
	if !ok || te.code != http.StatusNotImplemented || !strings.Contains(te.msg, "not set up") {
		t.Fatalf("notify without push: %v", err)
	}
}
