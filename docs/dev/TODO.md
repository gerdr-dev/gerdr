# TODO

Work that was looked into, measured and put off. Each entry says why it's worth doing and how, so it can be picked up
without redoing the investigation.

## Drop the heavy DNS SDKs from internal/acme

**Why**: they are a quarter of the backend binary, and every App update downloads the backend (docs/dev/UPDATE.md).
Measured on 2026-10-30 (`-s -w`, darwin/arm64):

| build | raw | gzip |
|---|---|---|
| all six providers | 37.7MB | 13.2MB |
| no provider SDKs | 28.0MB | 10.4MB |

Per provider: huaweicloud +1.5MB (also pulls in mongo-driver's bson), alidns +1.0MB, route53 +0.5MB (plus the
aws-sdk auth stack); cloudflare, digitalocean and tencentcloud cost ~0 (lego already talks plain HTTP to them).
Expected result: backend ~28MB, update package ~7.5MB brotli instead of ~9.7MB.

**How**: an ACME DNS-01 provider only creates one TXT record and deletes it (lego's `challenge.Provider`:
`Present` / `CleanUp`). Write those three over `net/http` with their request signing:

- alidns: RPC-style query string signed with HMAC-SHA1 (~80 lines).
- route53: AWS SigV4, XML body, ChangeResourceRecordSets + GetChange (~150 lines).
- huaweicloud: SDK-HMAC-SHA256 (APIG AK/SK) signing, find the zone first (~120 lines).

Keep lego for the ACME client itself (~3.5MB) and for the three cheap providers.

**Verifying without cloud credentials** (none were available): differential tests. Import the current SDK-based lego
providers only from `_test.go` (test-only imports don't reach the binary), point both them and the hand-written
ones at an `httptest` server, run Present / CleanUp, and compare method, path, query, body and signature headers.
Where the SDK signs with a random nonce / timestamp, take them from its request and feed them to ours: the
signatures must match byte for byte. Add the official signature examples too (AWS's SigV4 test suite, the examples
in Aliyun's and Huawei's signing docs). What this can't catch: the provider changing its API (the SDK would break
the same way). A real run against Let's Encrypt staging with real credentials is still the final check, whenever
someone has them.

After this, check what else grew the binary from 22MB (2026-10-28) to 37.7MB besides these SDKs.
