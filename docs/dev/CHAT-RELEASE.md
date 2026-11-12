# Releasing @gerdr/chat

`ui/packages/chat` is published to npm by GitHub Actions (`.github/workflows/publish-chat.yml`), not from a laptop.
npm trusts that workflow by name (trusted publishing, OIDC): no token is stored anywhere and there is no 2FA prompt
per release. Publishing by hand needed a security-key tap every time, and npm is restricting the tokens that skip 2FA
(account changes from 2026-08, direct publishing from 2027-01).

## A release

1. Bump `version` in `ui/packages/chat/package.json`, and commit it with what changed since the last version
   (`git log chat-v<last>..HEAD -- ui/packages/chat/src`; before tags existed, the last "chat 0.1.x" commit).
2. Tag that commit `chat-v<version>` and push the tag:

   ```
   git tag chat-v0.1.7 && git push origin chat-v0.1.7
   ```

3. The workflow checks the tag against package.json, builds (`prepublishOnly`: `node build.mjs`) and publishes with
   provenance. Check: `npm view @gerdr/chat version --prefer-online`.

A wrong tag (package.json says another version) fails before anything is published. A version already on npm can't
be published again: bump it.

## Set up once (done by the package's owner on npmjs.com)

npmjs.com → `@gerdr/chat` → Settings → Trusted Publisher → GitHub Actions: organization or user `gerdr-dev`,
repository `gerdr`, workflow filename `publish-chat.yml`, environment empty. After the first release goes through,
the same page can be set to disallow token publishing, so only this workflow can publish.
