#!/usr/bin/env bash
# Prints the code signing identity local builds sign with, or nothing (then builds stay ad-hoc, as before).
#
# Why: macOS privacy grants (Screen Recording, Accessibility — the remote screen needs both) are keyed by the
# binary's designated requirement. Ad-hoc signed, that is its cdhash, so every rebuild / update silently voids the
# grant while System Settings still shows it on. Signed with a fixed certificate, the requirement is
# `identifier … and certificate leaf = H"…"`, which survives rebuilds: grant once.
#
# The certificate is self-signed and lives in the login keychain. It needn't be trusted (codesign signs with it and
# TCC only compares the leaf); it is not a Developer ID, so Gatekeeper treats the App as before.
#   ./desktop/sign-id.sh          the identity name if it exists
#   ./desktop/sign-id.sh --create create it first when missing (make signing-id)
# GERDR_SIGN_ID overrides the name; GERDR_SIGN_ID=- forces ad-hoc.
set -euo pipefail
ID="${GERDR_SIGN_ID:-Gerdr Local Signing}"
[ "$(uname)" = Darwin ] || exit 0
[ "$ID" = - ] && exit 0
has() { security find-identity -p codesigning 2>/dev/null | grep -qF "\"$ID\""; }
if ! has && [ "${1:-}" = --create ]; then
  d=$(mktemp -d)
  trap 'rm -rf "$d"' EXIT
  cat >"$d/cfg" <<EOF
[req]
distinguished_name=dn
x509_extensions=ext
prompt=no
[dn]
CN=$ID
[ext]
basicConstraints=critical,CA:false
keyUsage=critical,digitalSignature
extendedKeyUsage=critical,codeSigning
EOF
  openssl req -x509 -newkey rsa:2048 -nodes -keyout "$d/k.pem" -out "$d/c.pem" -days 7300 -config "$d/cfg" 2>/dev/null
  # -legacy: the keychain can't read OpenSSL 3's default PKCS#12 encryption
  openssl pkcs12 -export -legacy -inkey "$d/k.pem" -in "$d/c.pem" -out "$d/id.p12" -passout pass:gerdr 2>/dev/null ||
    openssl pkcs12 -export -inkey "$d/k.pem" -in "$d/c.pem" -out "$d/id.p12" -passout pass:gerdr
  security import "$d/id.p12" -k "$HOME/Library/Keychains/login.keychain-db" -P gerdr -T /usr/bin/codesign >&2
fi
if has; then echo "$ID"; fi
