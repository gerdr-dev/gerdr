# Remote access

<sub>[简体中文](remote.zh-CN.md)</sub>

From your phone or another computer, through a cloud relay ([dialhome](https://github.com/zbysir/dialhome)), or
[directly](#direct-access-tailscale-frp-a-port-forward-instead-of-the-relay) over your own network (Tailscale, frp).
TLS terminates on this machine; the relay only forwards ciphertext. A new device gets in with a **pairing code** (only
someone at the machine can issue one), then adds a **passkey** so the next device doesn't need you at the machine.
The local token does **not** work remotely. Design and reasoning: [docs/dev/REMOTE.md](../dev/REMOTE.md).

One-time setup:

```bash
# in the dialhome web admin (https://relay.example.com/), invite this machine — or on the VPS:
dialhome invite gerdr.example.com

# on this machine: register and set up certificates (DNS-01, Cloudflare as the example)
gerdr remote join https://relay.example.com gerdr.example.com <invite> \
  --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…
```

It prints the DNS records to add (an A record to the relay, a CAA record), writes `~/.gerdr/remote/remote.json`, and
takes effect after restarting Gerdr (the macOS app reads it too). Then:

- **pair** — Settings → Remote access → issue a pairing code (or `gerdr remote pair`), scan it with your phone. Codes
  last 5 minutes and work once.
- **passkey** — in Gerdr on the phone, Settings → Remote access → add a passkey. Paired devices re-verify after 24
  hours without one.
- **revoke** — *Kick* in Settings, or `gerdr remote devices` / `gerdr remote revoke <id|all>` (`all` clears passkeys
  too). Milder: `gerdr remote reverify <id>` keeps the device but makes it re-verify on its next request.
- `gerdr remote status` shows the relay connection and certificate expiry.

Everything can also come from the environment (overrides `remote.json`): `GERDR_RELAY_URL`, `GERDR_RELAY_HOST`,
`GERDR_ACME_DNS`, `GERDR_<DNS credential>` (e.g. `GERDR_CLOUDFLARE_DNS_API_TOKEN`), `GERDR_ACME_EMAIL`, or your own
certificate via `GERDR_TLS_CERT` / `GERDR_TLS_KEY`; `GERDR_DEVICE_TTL_DAYS` (default 90, 0 = never expire),
`GERDR_REAUTH_HOURS` (default 24, 0 = never).

## direct access (Tailscale, frp, a port forward) instead of the relay

No relay: Gerdr serves its LAN port (7878) with your domain's certificate, under the same pairing-code / passkey
rules. How the phone reaches this Mac is your own network; Gerdr doesn't care which one. A certificate is still
needed (passkeys only work over HTTPS); DNS-01 gets one without this Mac being reachable from the internet.

**With Tailscale and no domain** (the least to set up):

1. Install Tailscale on the Mac and the phone, sign both into the same tailnet. In the Tailscale admin console's
   [DNS page](https://login.tailscale.com/admin/dns), turn on *HTTPS Certificates* (MagicDNS too, it's on by default).
2. Settings → Remote access → *Tailscale (no domain)* → Connect (or `gerdr remote direct --tailscale`). Gerdr uses
   this Mac's tailnet name and gets its certificate from Tailscale, which renews it.
3. On the phone, with Tailscale on, open `https://<machine>.<tailnet>.ts.net:7878` and pair as usual.

The name appears in public Certificate Transparency logs, as every Tailscale HTTPS name does. To move to a domain of
your own later, remove remote access and connect again (passkeys are bound to the name).

**With Tailscale and your own domain** (nothing open to the internet at all):

1. Install Tailscale on the Mac and the phone, sign both into the same tailnet.
2. Settings → Remote access → *Direct access*: enter the domain, leave *Port in the address* empty, connect. Then
   pick the DNS provider under *Certificate* (or `gerdr remote direct gerdr.example.com --dns cloudflare --env
   CLOUDFLARE_DNS_API_TOKEN=…`).
3. Add an A record: `gerdr.example.com → <this Mac's Tailscale address, 100.x.y.z>` (shown on the settings page and
   in `gerdr remote status`).
4. On the phone, with Tailscale on, open `https://gerdr.example.com:7878` and pair as usual.

Why that works without any proxy: the name itself points at the tailnet address, and Tailscale on the phone is a
system VPN, so the browser and the app both get to the Mac. With Tailscale off, the name leads nowhere; the Android
app still connects at home over the LAN, the browser doesn't. The port is in the address because macOS won't let
Gerdr take 443 on a specific address.

**With frp or a router's port forward** (facing the internet on 443):

1. Settings → Remote access → *Direct access*: the domain, *Port in the address* `443` (or `gerdr remote direct
   gerdr.example.com --url-port 443 …`).
2. The A record points at the frps machine (or your public IP). The address is `https://gerdr.example.com`.
3. frpc (frps with `vhostHTTPSPort = 443`):

   ```toml
   [[proxies]]
   name = "gerdr"
   type = "https"                          # passes TLS through untouched; never the https2http plugin
   customDomains = ["gerdr.example.com"]
   localIP = "127.0.0.1"
   localPort = 7878
   transport.proxyProtocolVersion = "v2"   # optional: lets Gerdr see the real client IP for its rate limits
   ```

Things to know: macOS may ask once to allow incoming connections for Gerdr (allow it, or nothing gets in, with no
error). With a port in the address, the Android app can't use passkeys in-app (Android checks the app on 443 only);
signing in goes through the browser approval instead, or approve the code on the Mac under Settings → Remote access →
*Approve app sign-in*.

The [Android app](../dev/ANDROID.md) wraps the same UI and adds what a browser can't: direct LAN connection when
you're home (same origin, same TLS), passkeys inside the app, and background notifications. The
[iOS app](../dev/IOS.md) does the same minus background notifications (LAN direct-connect needs iOS 17+).
