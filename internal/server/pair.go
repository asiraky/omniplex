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
<title>Pair this device — Omniplex</title>
<!-- Inline, like everything else here: the page has no assets behind it. -->
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100' role='img' aria-label='Omniplex'%3E%3Crect width='100' height='100' rx='22' fill='%23eef2ff' /%3E%3Crect x='44.3' y='21' width='11.5' height='16.1' rx='4' fill='%236366f1' /%3E%3Crect x='44.3' y='41.9' width='11.5' height='16.1' rx='4' fill='%23a855f7' /%3E%3Crect x='44.3' y='62.9' width='11.5' height='16.1' rx='4' fill='%23ec4899' /%3E%3C/svg%3E">
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
  .mark { display: block; height: 26px; width: auto; margin: 0 0 28px; color: oklch(0.94 0.005 285); }
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
  <svg class="mark" viewBox="3.3 -75.7 451.1 95.7" role="img" aria-label="Omniplex"><path fill="currentColor" d="M32.6 1.2L32.6 1.2Q24.5 1.2 17.9-2.5Q11.2-6.2 7.3-12.7Q3.3-19.1 3.3-27.3L3.3-27.3Q3.3-35.6 7.3-42Q11.2-48.4 17.9-52.1Q24.5-55.8 32.6-55.8L32.6-55.8Q40.7-55.8 47.3-52.1Q53.9-48.4 57.9-42Q61.8-35.6 61.8-27.3L61.8-27.3Q61.8-19.1 57.9-12.7Q53.9-6.2 47.3-2.5Q40.7 1.2 32.6 1.2ZM32.6-12.3L32.6-12.3Q36.7-12.3 39.8-14.2Q42.8-16.1 44.6-19.5Q46.3-22.9 46.3-27.3L46.3-27.3Q46.3-31.7 44.6-35.1Q42.8-38.4 39.8-40.4Q36.7-42.3 32.6-42.3L32.6-42.3Q28.5-42.3 25.4-40.4Q22.3-38.4 20.6-35.1Q18.8-31.7 18.8-27.3L18.8-27.3Q18.8-22.9 20.6-19.5Q22.3-16.1 25.4-14.2Q28.5-12.3 32.6-12.3ZM85.6 0L70.6 0L70.6-54.6L84.6-54.6L84.6-41.3L83.1-43.5Q84.3-49.8 88.9-52.8Q93.5-55.8 99.9-55.8L99.9-55.8Q106.7-55.8 111.9-52.4Q117.0-48.9 118.3-43.1L118.3-43.1L114.0-42.7Q116.7-49.4 121.7-52.6Q126.7-55.8 133.4-55.8L133.4-55.8Q139.3-55.8 143.9-53.2Q148.4-50.6 151-46.0Q153.6-41.3 153.6-35.1L153.6-35.1L153.6 0L138.6 0L138.6-31.9Q138.6-35.1 137.5-37.4Q136.3-39.7 134.2-41Q132.1-42.3 129.1-42.3L129.1-42.3Q126.2-42.3 124.1-41Q121.9-39.7 120.8-37.4Q119.6-35.1 119.6-31.9L119.6-31.9L119.6 0L104.6 0L104.6-31.9Q104.6-35.1 103.5-37.4Q102.3-39.7 100.2-41Q98.1-42.3 95.1-42.3L95.1-42.3Q92.2-42.3 90.1-41Q87.9-39.7 86.8-37.4Q85.6-35.1 85.6-31.9L85.6-31.9L85.6 0ZM178.5 0L163.5 0L163.5-54.6L177.5-54.6L177.5-43.8L176.7-46.2Q178.6-51.1 182.8-53.5Q187.1-55.8 192.8-55.8L192.8-55.8Q199-55.8 203.7-53.2Q208.3-50.6 210.9-46.0Q213.5-41.3 213.5-35.1L213.5-35.1L213.5 0L198.5 0L198.5-31.9Q198.5-35.1 197.3-37.4Q196-39.7 193.8-41Q191.5-42.3 188.5-42.3L188.5-42.3Q185.6-42.3 183.3-41Q181-39.7 179.8-37.4Q178.5-35.1 178.5-31.9L178.5-31.9L178.5 0ZM238.4 0L223.4 0L223.4-54.6L238.4-54.6L238.4 0ZM238.4-59.5L223.4-59.5L223.4-74.5L238.4-74.5L238.4-59.5ZM264.4 20L249.4 20L249.4-54.6L263.4-54.6L263.4-44.2L262.1-47.2Q264.8-51.3 269.4-53.6Q274.1-55.8 280.1-55.8L280.1-55.8Q287.9-55.8 294.2-52Q300.5-48.2 304.2-41.8Q307.9-35.3 307.9-27.3L307.9-27.3Q307.9-19.4 304.3-12.9Q300.6-6.4 294.3-2.6Q288 1.2 280 1.2L280 1.2Q274.4 1.2 269.6-0.9Q264.9-2.9 262-6.9L262-6.9L264.4-10L264.4 20ZM278.2-12.3L278.2-12.3Q282.4-12.3 285.6-14.2Q288.8-16.1 290.6-19.5Q292.4-22.9 292.4-27.3L292.4-27.3Q292.4-31.7 290.6-35.1Q288.8-38.4 285.6-40.4Q282.4-42.3 278.2-42.3L278.2-42.3Q274.2-42.3 271.1-40.4Q267.9-38.5 266.1-35.1Q264.4-31.7 264.4-27.3L264.4-27.3Q264.4-22.9 266.1-19.5Q267.9-16.1 271.1-14.2Q274.2-12.3 278.2-12.3ZM369 1.2L369 1.2Q360.3 1.2 353.9-2.7Q347.5-6.5 344-13Q340.5-19.5 340.5-27.4L340.5-27.4Q340.5-35.6 344.1-42Q347.8-48.4 354-52.1Q360.2-55.8 368-55.8L368-55.8Q374.5-55.8 379.5-53.8Q384.5-51.7 387.9-48Q391.4-44.3 393.2-39.5Q395-34.6 395-28.9L395-28.9Q395-27.3 394.9-25.8Q394.7-24.2 394.3-23.1L394.3-23.1L353.4-23.1L353.4-34.1L385.8-34.1L378.7-28.9Q379.7-33.2 378.6-36.6Q377.5-39.9 374.8-41.9Q372-43.8 368-43.8L368-43.8Q364.1-43.8 361.3-41.9Q358.5-40 357.1-36.3Q355.7-32.6 356-27.3L356-27.3Q355.6-22.7 357.1-19.2Q358.6-15.7 361.7-13.8Q364.8-11.8 369.2-11.8L369.2-11.8Q373.2-11.8 376.1-13.4Q378.9-15 380.5-17.8L380.5-17.8L392.5-12.1Q390.9-8.1 387.4-5.1Q384-2.1 379.3-0.5Q374.6 1.2 369 1.2ZM415.9 0L398.5 0L417.6-27.4L398.4-54.6L415.8-54.6L430.2-33.4L422.6-33.4L437-54.6L454.4-54.6L435.2-27.4L454.2 0L436.9 0L422.8-21.2L430-21.2L415.9 0Z"/><rect x="316.7" y="-75.7" width="15" height="21" rx="5.3" fill="#6366f1"/><rect x="316.7" y="-48.4" width="15" height="21" rx="5.3" fill="#a855f7"/><rect x="316.7" y="-21" width="15" height="21" rx="5.3" fill="#ec4899"/></svg>
  <h1>Pair this device</h1>
  <p>Enter the code shown in the terminal where Omniplex is running. You only do this once per device.</p>

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

	s.guard.SetCookie(w, r, token)
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
		s.guard.ClearCookie(w)
	}
	writeJSON(w, map[string]any{"revoked": id})
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
