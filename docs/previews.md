# Previews

A preview is a web service running inside a session's checkout — a dev server,
a Compose container's UI — published under a hostname you can open from
anywhere, with Omniplex's own auth in front of it.

The point is the phone. A dev server on port 5050 is trivially reachable from
the machine it runs on and completely unreachable from the train. Rendering
`http://localhost:5050` into the session panel would be honest and useless.

## How it is addressed

Each preview gets its own origin:

```
https://<preview-id>.agent.example.net
```

Not a path prefix under the main origin. An app that requests `/assets/app.js`
means the root of its own origin, and prefix rewriting cannot be made reliable
for arbitrary projects. A separate origin makes absolute paths, cookies,
redirects and WebSockets work with no rewriting at all.

The id is a single DNS label, always — `web-add-previews`, never
`web.add-previews`. A wildcard certificate matches exactly one level, so a
second dot would need a certificate no public CA will issue. Ids are readable
rather than opaque because on a phone the hostname is the only clue about
which worktree a tab belongs to, and readability costs nothing: the ticket
handshake, not obscurity, is what keeps strangers out.

## One-time setup

Everything below is done once. Adding a project, a worktree, or a service
afterwards needs no change here — that is the whole design goal.

### 1. DNS

A wildcard `A` record, alongside the existing `agent` record:

| Type | Name       | Value              |
|------|------------|--------------------|
| A    | `*.agent`  | *(the VPS's public IP)* |

### 2. A stable address for the Mac inside the tunnel

Caddy forwards to this machine over the OpenVPN tunnel, currently
`10.8.0.4`. OpenVPN hands that out from a pool, so it can move — and if it
moves, the Caddy config has to be edited, which is exactly what this feature
is supposed to make unnecessary.

The server runs `topology subnet` — the Mac's interface shows a
`255.255.255.0` netmask and `10.8.0.1` as its peer, where the legacy `net30`
topology would show a `/30` and an adjacent peer. That is what decides the
form of `ifconfig-push` below: address plus netmask, not the address pair
`net30` requires.

In `/etc/openvpn/server.conf`, an absolute path (a relative one resolves
against OpenVPN's working directory, which is a quiet way for this to do
nothing):

```
client-config-dir /etc/openvpn/ccd
ifconfig-pool 10.8.0.100 10.8.0.200
```

The pool line keeps dynamic assignment off the static address, since
`ifconfig-push` reserves nothing and another client could be handed
`10.8.0.4` first.

Then a file named for the client's **common name** exactly — not the `.ovpn`
filename — with no extension. Find the CN with
`grep -E '^(CLIENT_LIST|Common Name)' /var/log/openvpn/status.log`:

```sh
echo 'ifconfig-push 10.8.0.4 255.255.255.0' > /etc/openvpn/ccd/<cn>
chmod 644 /etc/openvpn/ccd/<cn>
chmod 755 /etc/openvpn/ccd
```

The permissions are not decoration. OpenVPN drops to `nobody` and reads CCD
files afterwards, so a root-only file means the client silently keeps its
pool address.

CCD files are read on each client connect, so that needs only a reconnect;
the `ifconfig-pool` change needs `systemctl restart openvpn@server`.

One ordering caveat: Omniplex binds its listeners once, at startup, so a
server that started before the tunnel came up is not listening on `10.8.0.4`
and Caddy answers 502. Bringing the VPN up first, or restarting Omniplex
after it, is the whole fix.

### 3. Caddy

The wildcard certificate needs a DNS-01 challenge — HTTP-01 cannot issue one —
which needs the Hetzner DNS plugin compiled into the binary. The Debian
package has no DNS providers, so it is replaced with a build that does:

```sh
cp /usr/bin/caddy /root/caddy-apt.bak
curl -fsSL -o /tmp/caddy \
  "https://caddyserver.com/api/download?os=linux&arch=arm64&p=github.com/caddy-dns/hetzner"
chmod +x /tmp/caddy
/tmp/caddy validate --config /etc/caddy/Caddyfile   # before anything is swapped
install -m 0755 /tmp/caddy /usr/bin/caddy
apt-mark hold caddy
```

The hold matters: without it the next `apt upgrade` quietly puts the
plugin-less binary back, and the wildcard certificate stops renewing months
later for no visible reason.

Create a DNS API token at <https://dns.hetzner.com/settings/api-token>. It goes
in a file, not in the unit — `Environment=` values are readable by anyone who
can run `systemctl show`:

```sh
install -m 600 -o caddy -g caddy /dev/null /etc/caddy/hetzner.env
echo 'HETZNER_DNS_API_TOKEN=...' > /etc/caddy/hetzner.env
```

and a drop-in at `/etc/systemd/system/caddy.service.d/hetzner.conf`:

```ini
[Service]
EnvironmentFile=/etc/caddy/hetzner.env
ExecStart=
ExecStart=/usr/bin/caddy run --config /etc/caddy/Caddyfile
```

`ExecStart` is overridden only to drop the package's `--environ` flag, which
prints the whole environment to stdout at startup and would put the token in
the journal.

Then one new block, leaving the existing `agent.example.net` block
exactly as it is:

```caddyfile
*.agent.example.net {
    tls {
        dns hetzner {env.HETZNER_DNS_API_TOKEN}
    }
    reverse_proxy 10.8.0.4:8788
}
```

Caddy forwards WebSocket upgrades through `reverse_proxy` without extra
configuration, which is what HMR needs.

An environment file is read at start, not at reload, so this needs
`systemctl daemon-reload && systemctl restart caddy` rather than a reload.
With the token missing or wrong, the wildcard is the only site that fails:
certificate management for each name is independent, and the log says
`hetzner: api token missing`.

### 4. Tell Omniplex the domain

Set `previewDomain` in the user config to `agent.example.net`. Empty —
the default — turns preview publishing off entirely: with no wildcard in
place a preview hostname would not resolve, and generating links that 404 is
worse than generating none.

## How a preview is found

Three sources, merged so the most deliberate name for a port wins:

1. **Declared** — the provision hook's `resources` result, in the shape the
   workspace lifecycle spec documents (`{"appUrl": "http://…:8204"}`). Trusted
   without probing, so a dev server that is slow to boot does not flicker out
   of the list.
2. **Docker** — published TCP ports of Compose containers whose
   `com.docker.compose.project.working_dir` label points into the checkout.
   The listening socket belongs to Docker's own networking process, so the
   label is the only link back to the worktree.
3. **Process** — loopback listeners whose owning process has its working
   directory in the checkout. By cwd rather than by walking down from the
   harness, because the interesting case is a server started and detached,
   which is reparented away immediately.

Every detected port is then asked whether it actually serves the web, and
dropped if not. This is not optional polish: one Compose project on this
machine publishes sixteen TCP ports, of which three are web UIs and the rest
are Postgres, pgbouncer, SIP and TURN. Declared services skip the probe.

UDP, unpublished ports and published *ranges* are all ignored. A forty-port
range is an RTP media allocation, never a web UI, and listing it would bury
the real link.

## Access

The device cookie is host-only and does not travel to a preview origin.
Widening it to the parent domain would hand every preview a credential that
controls the whole server, so a preview gets one of its own:

1. The link in the UI points at the **main** origin,
   `/api/previews/<id>/open`, where the device cookie authenticates normally.
   This is a plain link, so it survives a phone's popup blocker.
2. That endpoint mints a single-use ticket valid for 60 seconds and redirects
   to `https://<id>.agent.example.net/__omniplex/enter?t=…`.
3. The preview origin verifies and spends the ticket, sets a host-only cookie
   scoped to that one preview, and redirects to `/` so the ticket does not
   stay in the address bar.

The signing key lives in memory and is regenerated on restart, matching the
registry: a credential that outlived the preview it names would be a
credential pointing at whatever took the port next. Only a registered id
resolves at all, which is the line between a reverse proxy and an open one.

## What this does not do

HTTP and WebSocket only. Host-based routing needs the client to send a
hostname, which Postgres, Redis and raw TCP do not. Exposing those would mean
a listener per port and a Caddy edit each time — precisely what this avoids.
