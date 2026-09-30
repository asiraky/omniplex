// Package banner renders what the operator sees when the server starts:
// every address it is reachable on, and — when another device could connect —
// how to pair one.
package banner

import (
	"fmt"
	"io"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// QR renders a QR code using half-block characters, two rows of modules per
// line of text. A QR module is square, but a terminal cell is about twice as
// tall as it is wide, so drawing one module per cell produces a stretched code
// that many phone cameras refuse to read.
//
// The colours are inverted from the usual convention — foreground blocks stand
// for light modules — because terminals are usually dark and a scanner needs
// the quiet zone to be the lighter of the two.
func QR(text string) (string, error) {
	code, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", err
	}
	bitmap := code.Bitmap()

	// Bitmap already includes a quiet zone, so no margin is added here.
	var b strings.Builder
	for y := 0; y < len(bitmap); y += 2 {
		for x := range bitmap[y] {
			top := bitmap[y][x]
			bottom := false
			if y+1 < len(bitmap) {
				bottom = bitmap[y+1][x]
			}
			switch {
			case top && bottom:
				b.WriteRune(' ')
			case top:
				b.WriteRune('▄')
			case bottom:
				b.WriteRune('▀')
			default:
				b.WriteRune('█')
			}
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// Line is one labelled row in the address list.
type Line struct {
	Label string
	URL   string
	// Insecure marks an address whose traffic is not protected in transit, so
	// the operator can see which ones those are before handing one out.
	Insecure bool
}

// Options is everything the banner needs to know.
type Options struct {
	DBPath    string
	Projects  string
	Harness   []string
	Addrs     []Line
	HasUI     bool
	Reachable bool

	// PairingURL and PairingCode are shown only when another device could
	// actually connect. Printing a code on a loopback-only server would be
	// asking the operator to solve a problem they do not have.
	PairingURL string
	// PairingCode is the human-typeable form; PairingRaw is what the URL
	// fragment carries, kept apart so the printed address can omit it.
	PairingCode string
	PairingRaw  string
}

// Write prints the banner.
func Write(w io.Writer, o Options) {
	fmt.Fprintf(w, "\n  Omniplex — harness multiplexer\n\n")
	fmt.Fprintf(w, "  log       %s\n", o.DBPath)
	fmt.Fprintf(w, "  projects  %s\n", o.Projects)
	for _, h := range o.Harness {
		fmt.Fprintf(w, "  harness   %s\n", h)
	}

	fmt.Fprintln(w)
	insecure := false
	for _, a := range o.Addrs {
		mark := ""
		if a.Insecure {
			mark = "  (unencrypted)"
			insecure = true
		}
		fmt.Fprintf(w, "  %-15s %s%s\n", a.Label, a.URL, mark)
	}
	if insecure {
		// Not alarmism: on a network someone else controls, the pairing code
		// and then the device token are readable off the wire. The tailnet
		// address is not, which is the practical advice.
		fmt.Fprintf(w, "\n  Addresses marked unencrypted send traffic in the clear. On a network\n")
		fmt.Fprintf(w, "  you do not trust, prefer the Tailscale address.\n")
	}

	if !o.HasUI {
		fmt.Fprintf(w, "\n  No UI bundle embedded. Run the web app with:  cd web && npm run dev\n")
	}

	if o.Reachable && o.PairingCode != "" {
		// The address is printed as well as encoded: a narrow terminal
		// mangles the QR, and a code on its own does not say where to enter
		// it.
		fmt.Fprintf(w, "\n  Pair a device — scan this, or open\n")
		fmt.Fprintf(w, "  %s\n  and enter %s\n\n", strings.TrimSuffix(o.PairingURL, "#c="+o.PairingRaw), o.PairingCode)
		if qr, err := QR(o.PairingURL); err == nil {
			for _, line := range strings.Split(strings.TrimRight(qr, "\n"), "\n") {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
		fmt.Fprintf(w, "\n  The code is single-use and expires in 10 minutes.\n")
		fmt.Fprintf(w, "  Devices on this machine need no pairing.\n")
	}

	fmt.Fprintln(w)
}
