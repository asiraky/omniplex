# Releasing Omniplex

A release is a git tag. Pushing `vX.Y.Z` runs `.github/workflows/release.yml`, which builds the
desktop app and the server tarballs, signs and notarizes the app, and publishes one GitHub release.
Installed desktop apps pick it up through electron-updater, the download page links to it, and
`scripts/install.sh` installs it.

## Cutting a release

```bash
git checkout main && git pull
git tag v0.2.0
git push origin v0.2.0
```

The version comes from the tag. Nothing in the repo needs bumping first: `desktop/package.json`
stays at `0.0.0` in git and the workflow sets it for the build, and the server gets its version from
`-ldflags "-X main.version=..."`.

A version with a suffix, such as `v0.2.0-rc.1`, publishes as a GitHub prerelease. The download page
and `install.sh` follow the latest full release, and electron-updater skips prereleases by default,
so nobody gets an rc without asking for it.

### Dry run

Actions, Release, Run workflow, and give it a version. A manual run builds everything the tag would.
It signs if the secrets are set and builds unsigned if they are not, then keeps the results as
workflow artifacts for seven days. It creates no tag and no release. Do one before the first real
release, and again after touching the workflow or the desktop packaging.

### When a tag run fails

The release stays a draft, so nobody sees it. Fix the cause and re-run the failed jobs. The draft is
reused and uploads overwrite what is there. If the fix needs a code change, delete the draft and the
tag, then tag the fixed commit:

```bash
gh release delete v0.2.0 --yes
git push origin :refs/tags/v0.2.0 && git tag -d v0.2.0
```

A published version is never rebuilt. Tag the next one.

## What the workflow does

1. `prepare` reads the version from the tag and checks it is semver. On a tag it fails straight away
   if any signing secret is missing, because an unsigned app shows exactly the security warning the
   desktop app exists to avoid. Then it creates a draft release with generated notes.
2. `server` builds the bundled server (web UI and Claude sidecar compiled in, so the machine needs no
   Node) for darwin-arm64, darwin-amd64, linux-amd64 and linux-arm64 on one Ubuntu runner. Go
   cross-compiles because CGO is off and SQLite is modernc. `bun build --compile --target=...`
   cross-compiles the sidecar. Each target is packed as
   `omniplex-<version>-<os>-<arch>.tar.gz` holding `omniplex-<version>-<os>-<arch>/omniplex`, and all
   four go into `SHA256SUMS`. The job runs the linux-amd64 binary's `omniplex version` to check the
   stamp.
3. `desktop-mac` runs on one Apple Silicon runner (`macos-15`). It builds the server for both Mac
   architectures, stages them at `desktop/resources/bin/mac-arm64/omniplex` and
   `desktop/resources/bin/mac-x64/omniplex`, and runs electron-builder once with `--arm64 --x64`.
   One run matters: each electron-builder run writes its own `latest-mac.yml`, and two runs would
   overwrite each other's on the release, which breaks auto-update for one architecture. It is also
   half the macOS runner minutes of a separate Intel job. electron-builder signs with the Developer
   ID certificate, notarizes through the App Store Connect API key, and uploads the dmg, zip,
   blockmaps and `latest-mac.yml` into the draft.
4. `desktop-windows` does the same on `windows-latest` for x64, staging
   `desktop/resources/bin/win-x64/omniplex.exe` and signing with Azure Trusted Signing. It only runs
   when the repository variable `RELEASE_WINDOWS` is `true`. See [Turning on Windows](#turning-on-windows).
5. `publish` checks the draft has the files an updater and the download page need (`SHA256SUMS`, four
   tarballs, `latest-mac.yml`, two dmgs, and `latest.yml` plus an exe when Windows ran), then
   publishes it and marks it latest unless it is a prerelease.

`scripts/build-release.sh <goos> <goarch> <version> <out>` is the one place a release binary gets
built. Run it locally to reproduce a CI build after `npm ci && npm run build:web`.

Bun is pinned (`bun-version` in the workflow). Bump it deliberately and do a dry run, since the
sidecar carries Bun's runtime.

## One-time setup

All of these are repository secrets (Settings, Secrets and variables, Actions) except
`RELEASE_WINDOWS`, which is a variable. Setting them with the CLI keeps multi-line values intact:

```bash
gh secret set APPLE_API_KEY < AuthKey_ABC123XYZ.p8
gh variable set RELEASE_WINDOWS --body true
```

| Name | Kind | What it is |
|---|---|---|
| `CSC_LINK` | secret | Developer ID Application certificate and key, `.p12`, base64 |
| `CSC_KEY_PASSWORD` | secret | the `.p12` export password |
| `APPLE_API_KEY` | secret | contents of the App Store Connect API key, `AuthKey_<id>.p8` |
| `APPLE_API_KEY_ID` | secret | that key's Key ID |
| `APPLE_API_ISSUER` | secret | the Issuer ID shown above the keys list |
| `AZURE_TENANT_ID` | secret | Entra tenant of the signing app registration |
| `AZURE_CLIENT_ID` | secret | the app registration's application (client) ID |
| `AZURE_CLIENT_SECRET` | secret | a client secret on that registration |
| `AZURE_TRUSTED_SIGNING_ENDPOINT` | secret | the signing account's endpoint URI, e.g. `https://eus.codesigning.azure.net/` |
| `AZURE_TRUSTED_SIGNING_ACCOUNT` | secret | the signing account name |
| `AZURE_TRUSTED_SIGNING_PROFILE` | secret | the certificate profile name |
| `AZURE_TRUSTED_SIGNING_PUBLISHER` | secret | the certificate's subject CN, exactly as validated |
| `RELEASE_WINDOWS` | variable | `true` to build and require Windows |

The workflow hands the Azure names to electron-builder as `AZURE_SIGNING_ENDPOINT`,
`AZURE_SIGNING_ACCOUNT`, `AZURE_SIGNING_CERT_PROFILE` and `AZURE_SIGNING_PUBLISHER`, which is what
`desktop/electron-builder.config.cjs` reads.

### Apple: Developer ID certificate

You need a paid Apple Developer Program membership ($99 a year). The account holder has to create
the certificate.

1. In Xcode, Settings, Accounts, pick the team, Manage Certificates, then `+` and
   **Developer ID Application**. (Or create it at developer.apple.com, Certificates, from a CSR made in
   Keychain Access.)
2. In Keychain Access, under My Certificates, find "Developer ID Application: <name> (<team id>)".
   Expand it to check the private key is there. Right-click the certificate, Export, save as `.p12`
   with a strong password.
3. Store it:

   ```bash
   base64 -i DeveloperIDApplication.p12 | gh secret set CSC_LINK
   gh secret set CSC_KEY_PASSWORD      # paste the export password
   ```

4. Delete the `.p12` file. Keep the certificate in your keychain. It is valid for five years.

### Apple: notarization key

1. App Store Connect, Users and Access, Integrations, App Store Connect API, Team Keys. Generate a
   key with the **Developer** role.
2. Download `AuthKey_<id>.p8`. Apple lets you download it once.
3. Store it:

   ```bash
   gh secret set APPLE_API_KEY < AuthKey_<id>.p8
   gh secret set APPLE_API_KEY_ID --body <id>
   gh secret set APPLE_API_ISSUER --body <issuer uuid>
   ```

The workflow writes the key to a temporary file and passes electron-builder its path, which is what
notarytool wants.

### Windows: Azure Trusted Signing

Roughly $10 a month for the Basic tier. Microsoft has to validate your identity before it will issue
a certificate, and whether an individual (rather than a company) qualifies depends on your country.
Check that first; it is one of the open questions below.

1. In an Azure subscription, register the `Microsoft.CodeSigning` resource provider.
2. Create a Trusted Signing account. Its name is `AZURE_TRUSTED_SIGNING_ACCOUNT`, and the endpoint URI
   on its overview page (region-specific) is `AZURE_TRUSTED_SIGNING_ENDPOINT`.
3. On the account's Access control, give yourself **Trusted Signing Identity Verifier**. Then,
   under Identity validations, start a new **Public** validation (organization or individual) and
   wait for it to be approved. This takes days, not minutes.
4. Create a certificate profile of type **Public Trust** on that validated identity. Its name is
   `AZURE_TRUSTED_SIGNING_PROFILE`. The subject CN it shows, which is your validated legal name, is
   `AZURE_TRUSTED_SIGNING_PUBLISHER`. Copy it exactly: electron-updater on Windows refuses an update
   whose signer does not match.
5. In Microsoft Entra ID, create an app registration for CI and add a client secret. Tenant ID,
   application (client) ID and the secret value are `AZURE_TENANT_ID`, `AZURE_CLIENT_ID` and
   `AZURE_CLIENT_SECRET`. Client secrets expire (24 months at most), so put the date in a calendar.
6. Back on the Trusted Signing account's Access control, give that app registration
   **Trusted Signing Certificate Profile Signer**.

### Turning on Windows

The server does not compile for `GOOS=windows` until #132 lands. After it does:

1. Make the bundled sidecar executable on Windows. `scripts/build-release.sh` renames Bun's
   `omniplex-claude-sidecar.exe` to the extension-less name `bundled.go` embeds, so the server has to
   write the extracted copy with a `.exe` suffix on Windows, or Windows will not run it.
2. Set the Azure secrets above.
3. Set the variable, then do a dry run before the next tag. The Windows job only runs when the
   variable is `true`, dry runs included.

   ```bash
   gh variable set RELEASE_WINDOWS --body true
   ```

From then on a tag fails without the Azure secrets, and `publish` requires the Windows installer. The
download page switches from "Windows is coming soon" to a Windows button on its own once a release
has an `.exe`.

### Download page (GitHub Pages)

`site/` is a static page. It detects the visitor's OS and chip in the browser, asks the GitHub API
for the latest release, and links straight to the matching installer. If the API fails or
rate-limits (60 requests an hour per IP, unauthenticated), every link falls back to the releases
page. A release never needs a redeploy.

1. Settings, Pages, Source: **GitHub Actions**.
2. Push a change under `site/` to main, or run the Pages workflow by hand. The page is at
   `https://asiraky.github.io/omniplex/`.

For a custom domain later: add a CNAME record pointing the domain at `asiraky.github.io`, enter the
domain under Settings, Pages, and turn on Enforce HTTPS. Pages deployed from Actions do not need a
`CNAME` file in the repo. Update the links in the README once the domain is live.

## Terminal install

`scripts/install.sh` is the semi-technical path, macOS and Linux only:

```bash
curl -fsSL https://raw.githubusercontent.com/asiraky/omniplex/main/scripts/install.sh | sh
```

It fetches `SHA256SUMS` from the latest release (via GitHub's `releases/latest/download/` redirect,
so no API call), picks the tarball for the machine from it, verifies the hash, and installs
`omniplex` plus an `omni` symlink into `~/.local/bin`. Environment overrides:

- `OMNIPLEX_VERSION=0.2.0` installs that release.
- `OMNIPLEX_INSTALL_DIR=/usr/local/bin` changes the destination.
- `OMNIPLEX_DOWNLOAD_BASE=http://127.0.0.1:8000` reads assets from any directory holding
  `SHA256SUMS` and the tarballs. Use it to test the script against a local build:
  `python3 -m http.server -d dist` after building tarballs as the `server` job does.

The Mac tarballs are not signed. That is fine here: curl and tar do not set the quarantine
attribute, so Gatekeeper never checks them. Anyone who downloads a tarball through a browser gets
the warning, which is one more reason the desktop app is the path for everyone else.

## Open questions, and what the pipeline assumes meanwhile

- **Where the download page lives.** GitHub Pages at `asiraky.github.io/omniplex` until a domain is
  chosen. Moving it is a DNS record and a settings change.
- **Personal or company signing accounts.** The pipeline does not care, the secret names are the
  same. Decide before the first public release anyway: the Apple team and the Windows publisher name
  are baked into every installed copy, and the updaters refuse an update signed by someone else. A
  later switch means everyone reinstalls by hand. A company Apple account also needs a D-U-N-S
  number.
- **Intel Macs.** Built, because it costs nothing: the same runner and the same electron-builder run
  produce it. Drop `--x64` from the Mac job if nobody needs it.
- **Windows ARM.** Not built. Windows 11 on ARM runs the x64 installer under emulation, and the
  download page sends ARM visitors to it. Adding a native build means `--arm64` on the Windows job
  and a `win-arm64` server; `build-release.sh` already knows the target.
- **Linux desktop app.** Not built. Linux users get the server tarballs and `install.sh`.
