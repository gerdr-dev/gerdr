# Developer ID signing for the Mac App, TestFlight for iOS (needs the paid Apple account)

**Why**: the Mac dmgs are signed with the self-signed "Gerdr Local Signing" identity (release.yml "Signing identity",
desktop/sign-id.sh). That only keeps macOS privacy grants across updates; Gatekeeper still treats the App as unsigned,
so a first open needs the user to allow it by hand. iOS has no release at all: a free personal team signs for 7 days
and installs only from Xcode (IOS.md). A paid Apple Developer account ($99/year) fixes both, and both run in CI.

**Prerequisites** (account side, by hand): the paid account; a "Developer ID Application" certificate (only the
Account Holder can create it), exported as p12; an "Apple Distribution" certificate for iOS; an App Store Connect API
key (.p8 + key id + issuer id), shared by notarization and TestFlight uploads. All go into the repo's Secrets.

**Mac, how**:
1. Replace GERDR_MAC_SIGN_P12_B64 / _PASS with the Developer ID p12; the check `[ "$id" = "Gerdr Local Signing" ]` in
   release.yml and the identity name desktop/sign-id.sh defaults to follow (local builds can keep the self-signed one).
2. codesign in desktop/build.sh and desktop/package.sh with `--options runtime --timestamp` (notarization
   requires hardened runtime and a secure timestamp). Inner binaries first (Resources/gerdr, MacOS/gerdr-exp), the
   bundle last.
3. Notarize: `xcrun notarytool submit <dmg> --key AuthKey.p8 --key-id … --issuer … --wait`, then `xcrun stapler
   staple` the .app (before the zip / dmg are made) and the dmg.
4. The update-only backends (.br) get the same signature. The App downloads them itself, so they carry no quarantine
   flag and needn't be notarized.

**Mac, costs to accept once**:
- Every user re-grants Screen Recording / Accessibility / folder access: the grants are keyed by the certificate leaf,
  which changes. Afterwards it is better than now: a Developer ID designated requirement is `anchor apple generic …
  leaf[subject.OU] = <team id>`, so renewing the certificate no longer voids grants.
- Everyone downloads the whole App once: the signing setup is part of the shell id (UPDATE.md).
- Hardened runtime may break something that loads unsigned libraries or needs JIT; test remote screen, terminals and
  agents before release, add entitlements only for what actually breaks.

**iOS, how**: a release.yml job on macos-latest: `cd ios && xcodegen generate`, `xcodebuild archive` with automatic
signing (`-allowProvisioningUpdates` + the API key, so no profiles kept by hand), `xcodebuild -exportArchive` with
method app-store, upload with `xcrun altool --upload-app` (or fastlane pilot). Check the runner's Xcode can build
for the iOS SDK (IOS.md: Xcode 26 needs the iOS platform downloaded first).

**iOS limits that stay**: no .ipa on GitHub Releases for anyone to install like the apk. Distribution is TestFlight
(internal testers ≤100 without review, external after a light review, builds expire after 90 days), the App Store,
or Ad Hoc (registered UDIDs, 100 devices/year). Passkeys stay in Safari even with a paid team (IOS.md, "Why not
passkeys inside the App").
