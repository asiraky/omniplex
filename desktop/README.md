# Omniplex desktop

The Electron app that ships Omniplex as a one-click install on macOS and Windows. It runs the Go
server as a child process (`omniplex -private -port <port>`, env `OMNIPLEX_DESKTOP=1`), shows the
web UI in a window, and keeps the server alive from the menu bar / notification area when the
window is closed. Quitting from the tray stops the server (SIGTERM, then a kill after 5s).

## Layout

```
src/main.ts          app lifecycle: single instance, login item, wiring
src/server.ts        ServerSupervisor: spawn, health check, restart with backoff, stop
src/ports.ts         port choice: last used, 8787, 8788-8799, then any free port
src/shellPath.ts     login-shell PATH (macOS/Linux) merged with known tool dirs
src/restart.ts       restart backoff policy
src/routing.ts       first-run routing (/setup vs /) and navigation policy
src/window.ts        the one BrowserWindow (hardened; external links go to the browser)
src/tray.ts          tray menu
src/updater.ts       electron-updater against GitHub Releases (packaged builds only)
src/pages.ts         built-in loading and error pages
src/log.ts           log file (the server's stdout/stderr go there too)
src/state.ts         desktop-state.json in userData
scripts/build.mjs    esbuild bundle of src/main.ts into dist/main.cjs
scripts/icons.mjs    regenerates build/ and resources/icons/ from web/public/favicon.svg
build/               packaging inputs (app icons, mac entitlements)
resources/icons/     window and tray icons shipped in the app
resources/bin/       staged server binaries (gitignored), see below
electron-builder.config.cjs
```

## Scripts

From the repo root:

- `npm run build:desktop`: bundle the main process into `desktop/dist/`.
- `npm run test:desktop`: typecheck and unit tests.
- `npm run dist:desktop -- <electron-builder args>`: build and package.

From `desktop/`:

- `npm start`: build and run against a dev server binary (see Development).
- `npm run dist -- --mac --arm64 --x64`: package without publishing (`dist` passes
  `--publish never`).
- `npm run dist:mac`, `npm run dist:win`, `npm run dist:linux`: the per-platform shorthands.
- `npm run icons`: regenerate icons (needs Playwright's Chromium).

To publish to GitHub Releases, build and then call electron-builder directly with the config
file (this is what the release workflow does):

```
npm run build --workspace desktop
npm exec --workspace desktop -- electron-builder --config electron-builder.config.cjs --mac --arm64 --x64 --publish always
```

## Staging the server binary

electron-builder copies the server from

```
desktop/resources/bin/<os>-<arch>/omniplex        (mac, linux)
desktop/resources/bin/<os>-<arch>/omniplex.exe    (win)
```

into the app's `Resources/bin/`. `<os>` is electron-builder's name: `mac`, `win` or `linux`.
`<arch>` is `arm64` or `x64` (not Go's `amd64`). So a macOS release stages
`resources/bin/mac-arm64/omniplex` and `resources/bin/mac-x64/omniplex`, and a Windows release
stages `resources/bin/win-x64/omniplex.exe`.

Stage a bundled build (web UI and sidecar embedded, as `npm run build:bundled` produces, or
`scripts/build-release.sh`). Packaging fails if the binary for a target is missing.

Build both Mac architectures in one electron-builder run (`--mac --arm64 --x64`). That run writes
a single `latest-mac.yml` that lists both zips, which is what the updater reads. Separate runs per
architecture would overwrite each other's manifest.

## Version

The app version is `version` in `desktop/package.json`. It is `0.0.0` in git; the release
workflow sets it from the tag before packaging:

```
cd desktop && npm version --no-git-tag-version 1.2.3
# or, from the root: npm pkg set version=1.2.3 --workspace desktop
```

## Artefacts

In `desktop/release/`:

- `Omniplex-<version>-mac-arm64.dmg`, `Omniplex-<version>-mac-x64.dmg`
- `Omniplex-<version>-mac-arm64.zip`, `Omniplex-<version>-mac-x64.zip` (used by the updater)
- `Omniplex-<version>-win-x64.exe` (NSIS, per-user, one click)
- `Omniplex-<version>-linux-<arch>.AppImage`
- `latest-mac.yml`, `latest.yml`, `latest-linux.yml` (updater manifests; publish them with the
  artefacts)

## Signing

Without these variables, builds still succeed: macOS builds are signed ad hoc (Gatekeeper blocks
them) and Windows builds are unsigned (SmartScreen warns). Set `OMNIPLEX_REQUIRE_SIGNING=1` to
make a missing signature fail the build instead; the release workflow does this when it
publishes.

macOS (Developer ID, hardened runtime, notarization; the server binary is signed with the app's
entitlements):

- `CSC_LINK`: the Developer ID Application certificate, a .p12 as a path or base64.
- `CSC_KEY_PASSWORD`: its password. (`CSC_NAME` instead of `CSC_LINK` picks a certificate
  already in the keychain.)
- `APPLE_API_KEY`: path to the App Store Connect API key `.p8` file.
- `APPLE_API_KEY_ID`, `APPLE_API_ISSUER`: the key's ID and issuer ID.

Notarization runs only when a certificate is configured.

Windows (Azure Trusted Signing). All four `AZURE_SIGNING_*` variables must be set together:

- `AZURE_SIGNING_ENDPOINT`: e.g. `https://eus.codesigning.azure.net/`.
- `AZURE_SIGNING_ACCOUNT`: the Trusted Signing account name.
- `AZURE_SIGNING_CERT_PROFILE`: the certificate profile name.
- `AZURE_SIGNING_PUBLISHER`: the certificate's subject CN. The updater checks installers against
  it.
- `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`: the service principal the signing
  module authenticates as.

Publishing to GitHub Releases needs `GH_TOKEN`.

## Development

`npm start` in `desktop/` runs the app unpackaged. The server binary is `OMNIPLEX_SERVER_BIN`, or
`./omniplex` at the repo root (`npm run build:server`). Useful variables:

- `OMNIPLEX_SERVER_BIN`: the server binary to run.
- `OMNIPLEX_PORT`: preferred port (default 8787; the app moves to the next free one if taken).
- `OMNIPLEX_DB`: passed through to the server. Point it at a scratch file so a dev run does not
  share the database of an Omniplex you are already running.

Unpackaged runs log to the terminal as well as the log file, skip the updater, and never register
a login item.

The log is `omniplex.log` in Electron's logs directory: `~/Library/Logs/Omniplex` on macOS,
`%APPDATA%\Omniplex\logs` on Windows, `~/.config/Omniplex/logs` on Linux. The error page has a
button that reveals it.
