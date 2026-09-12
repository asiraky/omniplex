package preview

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"sync"
	"time"
)

// probeTimeout bounds one probe. A loopback server that has not answered in
// this long is either not HTTP or too wedged to link to.
const probeTimeout = 400 * time.Millisecond

// probeConcurrency bounds how many ports are probed at once. Detection can
// turn up a few dozen candidates on a machine running several Compose
// projects, and opening all of them simultaneously is rude to the machine for
// no gain.
const probeConcurrency = 8

// probe reports whether a loopback port serves the web, and over which scheme.
//
// This is what separates a link worth showing from a port that merely exists.
// Detection alone is far too noisy to put in front of a person: one of this
// machine's Compose projects publishes sixteen TCP ports, of which three are
// web UIs and the rest are Postgres, pgbouncer, SIP and TURN. Offering all
// sixteen as "open in browser" would make the feature worthless. Asking each
// port whether it speaks HTTP is the only honest way to tell them apart short
// of making the user configure it.
func probe(ctx context.Context, port int) (string, bool) {
	// TLS first, because the plaintext test cannot tell the difference. A
	// Caddy container on 443 answers a plaintext GET with "HTTP/1.1 400 Bad
	// Request — Client sent an HTTP request to an HTTPS server", which starts
	// with HTTP/ and passes the check below while being entirely unusable as
	// an http:// target. Verified against this machine's own containers.
	if serves := probeTLS(ctx, port); serves {
		return "https", true
	}
	if serves := probePlain(ctx, port); serves {
		return "http", true
	}
	return "", false
}

// probeTLS reports whether the port completes a TLS handshake. The
// certificate is not verified: a dev server's certificate is self-signed
// essentially always, and rejecting it would defeat the purpose.
func probeTLS(ctx context.Context, port int) bool {
	conn, err := dial(ctx, port)
	if err != nil {
		return false
	}
	defer conn.Close()

	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)

	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"})
	return tlsConn.HandshakeContext(ctx) == nil
}

// probePlain reports whether the port answers a plaintext HTTP request.
//
// The request is deliberately HTTP/1.0 with no keep-alive, so a server that
// answers is free to close immediately and we never hold a connection open.
func probePlain(ctx context.Context, port int) bool {
	conn, err := dial(ctx, port)
	if err != nil {
		return false
	}
	defer conn.Close()

	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write([]byte("GET / HTTP/1.0\r\nHost: localhost\r\nUser-Agent: omniplex-preview-probe\r\n\r\n")); err != nil {
		return false
	}

	// "HTTP/" is enough: any status line, including 404 or 500, proves there
	// is a web server there. Whether the app's root route happens to exist is
	// not our business.
	buf := make([]byte, 5)
	n, err := conn.Read(buf)
	if err != nil || n < 5 {
		return false
	}
	return string(buf) == "HTTP/"
}

func dial(ctx context.Context, port int) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

// keepServed filters candidates down to those that serve the web, stamping
// each with the scheme it answered on and preserving order.
//
// Declared services skip the probe: someone named them on purpose, and a dev
// server that is slow to boot should not vanish from the list because it was
// not listening yet on this pass. A declared entry keeps whatever scheme its
// URL carried.
func keepServed(ctx context.Context, found []Found) []Found {
	schemes := make([]string, len(found))
	sem := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup

	for i, f := range found {
		if f.Source == SourceDeclared {
			schemes[i] = f.Scheme
			continue
		}
		wg.Add(1)
		go func(i, port int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if scheme, ok := probe(probeCtx, port); ok {
				schemes[i] = scheme
			}
		}(i, f.Port)
	}
	wg.Wait()

	kept := make([]Found, 0, len(found))
	for i, f := range found {
		if schemes[i] == "" {
			continue
		}
		f.Scheme = schemes[i]
		kept = append(kept, f)
	}
	return kept
}
