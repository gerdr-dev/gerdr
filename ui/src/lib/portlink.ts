import { api } from "./api";
import { isApp, openInBrowser } from "./android";

/**
 * Local ports through the relay (internal/portproxy, docs/dev/REMOTE.md "Local ports through the relay"): an agent
 * says "open http://localhost:8765/x", and on a phone that is the phone's own localhost. When this page came
 * through the relay and the backend serves ports there (hello `portDomain`), opening such a link goes to
 * `https://8765.<that>/x`, which signs the device in through Gerdr and proxies to 127.0.0.1:8765 on the computer.
 *
 * Only http(s) links to loopback change. The backend sends portDomain only to remote pages, so on the computer
 * running Gerdr, where localhost is right, nothing changes.
 */
let domain = "";

/** sync.ts, from /api/hello ("" or missing = no port proxy, or not a remote page). */
export function setPortDomain(d: string | undefined) {
  domain = d ?? "";
}

const LOOPBACK = new Set(["localhost", "127.0.0.1", "0.0.0.0", "[::1]"]);

/** The local port and path an href points at, when it should go through the port proxy. */
export function portTarget(href: string): { port: number; path: string } | undefined {
  if (!domain) return undefined;
  let u: URL;
  try {
    u = new URL(href);
  } catch {
    return undefined;
  }
  if ((u.protocol !== "http:" && u.protocol !== "https:") || !LOOPBACK.has(u.hostname) || !u.port) return undefined;
  const port = Number(u.port);
  if (!(port >= 1024 && port <= 65535)) return undefined; // the server's line too (portproxy.Allowed)
  return { port, path: u.pathname + u.search + u.hash };
}

/** The address a loopback href has on its port subdomain (href itself when it doesn't go through the proxy). */
export function portLink(href: string): string {
  const t = portTarget(href);
  return t ? `https://${t.port}.${domain}${t.path}` : href;
}

/**
 * Open a loopback link through the port proxy; false when href isn't one (the caller opens it as usual).
 *
 * In a browser: the main site's /_port/<port>, which this browser is signed in to, mints the ticket and goes on.
 * In the phone Apps links open in the system browser, which has no device cookie: the ticket is minted here, with
 * the App's cookie, and the address carrying it is what the browser gets.
 */
export function openPortLink(href: string): boolean {
  const t = portTarget(href);
  if (!t) return false;
  if (isApp()) {
    api.portLink(t.port, t.path).then(
      (r) => openInBrowser(r.url),
      () => openInBrowser(portLink(href)), // older backend / proxy just went off: the plain address, sign-in there
    );
    return true;
  }
  window.open(`/_port/${t.port}?to=${encodeURIComponent(t.path)}`, "_blank", "noopener");
  return true;
}
