package preview

import "testing"

// The address a preview should be opened at depends entirely on how the user
// reached Omniplex, and getting it wrong is not a cosmetic bug: it produces a
// link that either resolves nowhere or routes loopback traffic through a
// cloud VPS.
func TestURLForPicksTheOriginTheUserArrivedOn(t *testing.T) {
	reg := NewRegistry()
	auth, err := NewAuth()
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(reg, auth, "agent.example.net", nil)
	p := Preview{ID: "web-inbox", Port: 5050, Scheme: "http"}

	tests := []struct {
		name string
		host string
		want string
	}{
		{"through the proxy", "agent.example.net", "https://web-inbox.agent.example.net"},
		{"already on a preview", "other.agent.example.net", "https://web-inbox.agent.example.net"},
		{"on the machine itself", "127.0.0.1:8788", "http://127.0.0.1:5050"},
		{"over the LAN", "192.168.1.20:8788", "http://192.168.1.20:5050"},
		{"over the tunnel", "10.8.0.4:8788", "http://10.8.0.4:5050"},
		{"an unrelated hostname", "laptop.local:8788", "http://laptop.local:5050"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := router.URLFor(tc.host, p); got != tc.want {
				t.Errorf("URLFor(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

// With no wildcard configured there is no publishable address, and inventing
// one would hand the user a link that cannot resolve.
func TestURLForWithoutADomainStaysDirect(t *testing.T) {
	reg := NewRegistry()
	auth, _ := NewAuth()
	router := NewRouter(reg, auth, "", nil)
	p := Preview{ID: "web-inbox", Port: 5050, Scheme: "http"}

	got := router.URLFor("agent.example.net", p)
	if want := "http://agent.example.net:5050"; got != want {
		t.Errorf("URLFor = %q, want %q", got, want)
	}
	if router.Published(got) {
		t.Error("a direct URL must not be treated as one we serve")
	}
}

// Published decides whether the ticket handshake is needed. A direct address
// must not get one — there is no Omniplex in that path to redeem it.
func TestPublishedDistinguishesServedFromDirect(t *testing.T) {
	reg := NewRegistry()
	auth, _ := NewAuth()
	router := NewRouter(reg, auth, "agent.example.net", nil)

	if !router.Published("https://web-inbox.agent.example.net") {
		t.Error("a published preview URL should need the handshake")
	}
	for _, direct := range []string{
		"http://127.0.0.1:5050",
		"http://192.168.1.20:5050",
		"https://192.168.1.20:5050",
	} {
		if router.Published(direct) {
			t.Errorf("%q should not need the handshake", direct)
		}
	}
}

// An https dev server reached directly must be linked as https, or the
// browser talks plaintext to a TLS port and the user sees a broken page.
func TestURLForKeepsTheServiceScheme(t *testing.T) {
	reg := NewRegistry()
	auth, _ := NewAuth()
	router := NewRouter(reg, auth, "", nil)

	p := Preview{ID: "api", Port: 8443, Scheme: "https"}
	if got, want := router.URLFor("192.168.1.20:8788", p), "https://192.168.1.20:8443"; got != want {
		t.Errorf("URLFor = %q, want %q", got, want)
	}
}
