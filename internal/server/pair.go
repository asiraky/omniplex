package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/auth"
)

// pairPage is served by Go rather than the React bundle on purpose. It is the
// one page an unpaired device can reach, so it must not depend on the app
// loading, and keeping it self-contained keeps the pre-auth surface to a
// single handler with no assets behind it.
//
// The code arrives in the URL fragment, never the query string: fragments are
// not sent to the server, so a pairing link cannot end up in an access log, a
// proxy log, or a Referer header on the way to somewhere else.
const pairPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="referrer" content="no-referrer">
<meta name="robots" content="noindex, nofollow">
<meta name="color-scheme" content="dark">
<title>Pair this device — omniplex</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; min-height: 100dvh;
    display: flex; align-items: center; justify-content: center;
    padding: max(24px, env(safe-area-inset-top)) 20px max(24px, env(safe-area-inset-bottom));
    background: oklch(0.16 0.006 285); color: oklch(0.94 0.005 285);
    font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .card { width: 100%; max-width: 380px; }
  h1 { font-size: 20px; font-weight: 600; letter-spacing: -0.02em; margin: 0 0 6px; }
  p  { margin: 0 0 20px; color: oklch(0.58 0.012 285); font-size: 14px; }
  label { display: block; font-size: 11px; text-transform: uppercase;
          letter-spacing: 0.05em; color: oklch(0.58 0.012 285); margin-bottom: 8px; }
  input {
    width: 100%; padding: 14px; border-radius: 10px;
    border: 1px solid oklch(0.27 0.008 285); background: oklch(0.20 0.006 285);
    color: inherit; font: 500 17px/1 ui-monospace, "SF Mono", Menlo, monospace;
    letter-spacing: 0.12em; text-align: center; text-transform: uppercase;
  }
  input:focus { outline: none; border-color: oklch(0.72 0.15 258); }
  button {
    width: 100%; margin-top: 12px; padding: 14px; border: 0; border-radius: 10px;
    background: oklch(0.72 0.15 258); color: oklch(0.16 0.006 285);
    font: 600 15px/1 inherit; cursor: pointer; min-height: 48px;
  }
  button:disabled { opacity: 0.5; cursor: default; }
  .msg { margin-top: 14px; padding: 12px; border-radius: 10px; font-size: 13px; display: none; }
  .msg.err { display: block; background: oklch(0.28 0.09 25); color: oklch(0.85 0.09 25); }
  .msg.ok  { display: block; background: oklch(0.28 0.08 155); color: oklch(0.86 0.09 155); }
  .spin { display: none; text-align: center; color: oklch(0.58 0.012 285); font-size: 14px; }
  .spin.on { display: block; }
  .form.hide { display: none; }
</style>
</head>
<body>
<div class="card">
  <h1>Pair this device</h1>
  <p>Enter the code shown in the terminal where omniplex is running. You only do this once per device.</p>

  <div class="spin" id="spin">Pairing…</div>

  <form class="form" id="form" autocomplete="off">
    <label for="code">Pairing code</label>
    <input id="code" name="code" inputmode="latin" autocapitalize="characters"
           autocorrect="off" spellcheck="false" placeholder="XXXX-XXXX-XXXX-XXXX" required>
    <button type="submit" id="go">Pair</button>
  </form>

  <div class="msg" id="msg"></div>
</div>
<script>
(function () {
  var form = document.getElementById("form");
  var input = document.getElementById("code");
  var button = document.getElementById("go");
  var msg = document.getElementById("msg");
  var spin = document.getElementById("spin");

  function show(text, kind) {
    msg.textContent = text;
    msg.className = "msg " + kind;
  }

  function pair(code) {
    button.disabled = true;
    msg.className = "msg";
    return fetch("/api/pair", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ code: code, label: navigator.userAgent })
    }).then(function (res) {
      return res.json().then(function (body) { return { ok: res.ok, body: body }; });
    }).then(function (r) {
      if (!r.ok) throw new Error((r.body && r.body.error) || "Pairing failed");
      show("Paired. Taking you in…", "ok");
      // Replace rather than assign so the fragment, which still holds the
      // code, does not stay in history.
      location.replace("/");
    }).catch(function (err) {
      button.disabled = false;
      spin.classList.remove("on");
      form.classList.remove("hide");
      show(err.message, "err");
    });
  }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var code = input.value.trim();
    if (code) pair(code);
  });

  // A scanned QR lands here with the code in the fragment. Clear it from the
  // address bar immediately so a screenshot or a shared link cannot leak it.
  var hash = location.hash.replace(/^#/, "");
  var params = new URLSearchParams(hash);
  var fromLink = params.get("c");
  if (fromLink) {
    history.replaceState(null, "", location.pathname);
    form.classList.add("hide");
    spin.classList.add("on");
    pair(fromLink);
  } else {
    input.focus();
  }
})();
</script>
</body>
</html>`

func (s *Server) handlePairPage(w http.ResponseWriter, r *http.Request) {
	// Already paired, or local: there is nothing to do here.
	if _, ok := s.guard.Authorize(r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write([]byte(pairPage))
}

// handlePair redeems a pairing code. It is the only unauthenticated endpoint
// that changes anything, which is why it is rate limited by peer.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	// An unauthenticated caller must not be able to hold a goroutine and a
	// socket open indefinitely by trickling a body. MaxBytesReader bounds the
	// size but not the time, so bound the time here.
	//
	// The deadline is set per handler rather than as a server-wide ReadTimeout
	// because the same server carries WebSockets, which are long-lived by
	// design and a blanket read deadline would kill them.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetReadDeadline(time.Now().Add(10 * time.Second))
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}

	var body struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request")
		return
	}

	token, device, err := s.guard.Redeem(r.Context(), auth.PeerKey(r), body.Code, deviceLabel(body.Label))
	switch {
	case errors.Is(err, auth.ErrTooManyAttempts):
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		// Every failure looks the same from outside, so guessing reveals
		// nothing about which part was wrong.
		writeError(w, http.StatusUnauthorized, auth.ErrBadCode.Error())
		return
	}

	auth.SetCookie(w, r, token)
	writeJSON(w, map[string]any{"device": device})
}

// deviceLabel turns a user agent into something recognisable in a device list,
// because "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5…)" is not.
func deviceLabel(ua string) string {
	if ua == "" {
		return ""
	}
	lower := strings.ToLower(ua)
	// Order matters: an iPad reports itself as Macintosh on recent iPadOS, so
	// the specific devices have to be tested before the desktop families.
	for _, rule := range []struct{ needle, label string }{
		{"iphone", "iPhone"},
		{"ipad", "iPad"},
		{"android", "Android device"},
		{"macintosh", "Mac"},
		{"windows", "Windows PC"},
		{"linux", "Linux machine"},
	} {
		if strings.Contains(lower, rule.needle) {
			return rule.label
		}
	}
	return "paired device"
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.guard.Devices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	current, _ := auth.DeviceFrom(r.Context())
	writeJSON(w, map[string]any{"devices": devices, "current": current.ID})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.guard.Revoke(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The token is gone from the database, but a socket it already opened was
	// only authorised once, at upgrade. Cut it.
	s.closeDevice(id)
	// Revoking the device you are using should also drop your own cookie,
	// so the browser does not keep presenting a token that no longer exists.
	if current, ok := auth.DeviceFrom(r.Context()); ok && current.ID == id {
		auth.ClearCookie(w)
	}
	writeJSON(w, map[string]any{"revoked": id})
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
