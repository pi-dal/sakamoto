# Desktop signing and releases

Regular pull-request CI builds ad-hoc macOS apps and unsigned Windows/Linux
clients without accessing release credentials. The separate
`.github/workflows/desktop-release.yml` signs and notarizes macOS releases and
packages all six targets. `workflow_dispatch` validates signed builds without
publishing; pushing `desktop-v<source-version>` publishes a desktop release and
updates the Homebrew tap. Desktop releases do not replace the latest CLI release.

## Credentials

Repository variable:

| Name | Value |
| --- | --- |
| `MACOS_TEAM_ID` | Expected Apple Developer Program team ID |

Repository secrets:

| Name | Purpose |
| --- | --- |
| `MACOS_CERTIFICATE_P12_BASE64` | Base64 PKCS#12 with a Developer ID Application certificate and its private key |
| `MACOS_CERTIFICATE_PASSWORD` | Password protecting that PKCS#12 |
| `ASC_PRIVATE_KEY` | App Store Connect API `.p8` key content for notarytool |
| `ASC_KEY_ID` | API key ID |
| `ASC_ISSUER_ID` | API issuer UUID |
| `HOMEBREW_TAP_SSH_KEY` | Dedicated write deploy key for `pi-dal/homebrew-tap` |

Apple Development and Apple Distribution certificates cannot replace Developer
ID Application. No provisioning profile or App Store app record is required for
this non-sandboxed app. Use the Account Holder to create the certificate in
[Apple Developer Certificates](https://developer.apple.com/account/resources/certificates/add).
If the API returns `This operation can only be performed by the Account Holder`,
complete certificate issuance in that account using a locally generated CSR. Keep
the matching private key outside the repository. Apple returns only a certificate;
it does not return the private key needed for signing.

The configured Notary API key must belong to the signing team and have permission
to submit notarization requests. The first signed workflow run validates that
access; a successful App Store Connect API request alone is insufficient evidence.

Upload secrets via stdin or protected files; do not put values in shell history:

```sh
base64 < /secure/location/application.p12 | gh secret set MACOS_CERTIFICATE_P12_BASE64
gh secret set MACOS_CERTIFICATE_PASSWORD < /secure/location/p12-passphrase.txt
gh secret set ASC_PRIVATE_KEY < /secure/location/AuthKey.p8
```

Use `gh secret set ASC_KEY_ID`, `gh secret set ASC_ISSUER_ID` and
`gh variable set MACOS_TEAM_ID` for the remaining Apple identifiers. A dedicated
Homebrew deploy key limits cross-repository writes to the tap; the workflow does
not need a personal access token. The SSH public key is registered on the tap and
the private key is stored only in the application repository secret.

## Verification and publication

The macOS job imports the certificate into a temporary keychain, restricts signing
to the expected team, and restores the original keychain search list even if the
build fails. It signs the bundled rescue helper, CLI and app from the inside out
with hardened runtime and secure timestamps. Each architecture is submitted to
Apple separately; only an `Accepted` result proceeds. Stapler and Gatekeeper
checks run before packaging. Final ZIPs are extracted again and checked for code
signatures, architecture, bundle version, stapled ticket and Gatekeeper acceptance.
Notarization rejections retain public build diagnostics as CI artifacts.

The publish job combines the six target manifests, verifies source commit and
actual archive hashes, then uploads a draft release before making it public.
The Homebrew job downloads the public macOS ZIPs and receipts, repeats release
verification, generates `Casks/sakamoto-desktop.rb`, audits it, and pushes it to the
tap using the deploy key. Hashes therefore describe the final stapled downloads.
The cask has architecture-specific URLs/checksums and macOS 12+ support; it installs
only `Sakamoto.app`. It does not install services, start a tunnel or remove config.

Run a signed build without publication first:

```sh
gh workflow run desktop-release.yml --ref YOUR_BRANCH
```

After that run succeeds, tag the same source version and push the tag:

```sh
git tag desktop-v0.2.18
git push origin desktop-v0.2.18
```

The version must match `cmd/sakamoto/main.go`. Release workflow runs are serialized.
Rerunning a failed tap job is supported; an unchanged cask produces no commit.
After a successful tap update, install with:

```sh
brew install --cask pi-dal/tap/sakamoto-desktop
```

The tap CI audits and installs the cask, verifies its signature/ticket and does not
launch the installed application. CLI formula installation remains independent.

## Local verification

```sh
mise exec -- python scripts/build-desktop.py --target darwin-arm64 --target darwin-amd64
mise exec -- python scripts/verify-desktop-release.py --allow-ad-hoc --cask-output dist/desktop/sakamoto-desktop.rb
```

This writes `dist/desktop/verification.json` with policy `local-ad-hoc`; it exercises
real archive extraction, architecture checks, bundled code verification and cask
generation. It cannot pass the release policy or publish Homebrew. Omitting
`--allow-ad-hoc` requires an expected team and actual signed/notarized archives.

For a local release build with a Developer ID identity installed and a stored
notarytool profile:

```sh
mise exec -- python scripts/build-desktop.py --target darwin-arm64 --target darwin-amd64 \
  --sign-identity 'Developer ID Application: Your Name (TEAMID)' \
  --team-id TEAMID --notarize --notary-profile sakamoto
mise exec -- python scripts/verify-desktop-release.py --team-id TEAMID
```
