# Several computers from one Gerdr (hosts)

Goal: the Gerdr you sit in front of (the **console**) shows and drives the sessions of your other computers
(**hosts**: a Mac mini, a server) next to its own, in the same workspaces and tabs, and its agents can message
theirs. Decided with the user (2026-11-03): **star topology, one-way trust** — the console controls hosts, a host
can't control the console or other hosts.

## The model: a host is a property of a session, not a project

A session is *host + directory + agent*. Projects stay what they are (an isolated set of workspaces); a workspace can
hold a local Claude tab next to a Codex tab on the Mac mini. Isolating hosts into projects was rejected: agents on
different hosts must be able to talk to each other, and projects exist to stop exactly that.

Every Gerdr stays autonomous: the agent runs on its host, its log, its files and its Claude / Codex transcripts stay
there. The console moves nothing; it is a window plus a router.

## Federation lives in the console's backend

```
 UI (browser / App) ──/api, /ws──▶ console gerdr ──https + device cookie──▶ host gerdr (its remote port)
 agent on console ──gerdr session──┘        │  merges host sessions into its list as "<host>~<id>"
                                           └─ routes ops on "<host>~…" to that host, rewrites ids both ways
```

Why the backend and not the browser talking to each host directly:

- The console's agents only know the console's Gerdr (`GERDR_DIR`, token). Routing in the backend makes
  `gerdr session list / send / new` reach other hosts with no new CLI concept.
- One origin for the UI: no cross-origin cookies, no second pairing per browser / phone. The phone that reaches the
  console reaches every host through it.
- The UI keeps treating a session id as an opaque string. It only learns `host` on the summary for the badge and the
  new-session picker.

### Session ids

A host session shows up on the console as `<hostID>~<id>` (`internal/peer.Join` / `Split`). Gerdr's own ids are
UUIDs and host ids are `[a-z0-9-]`, so `~` can't occur in either; it is URL-safe, so `/api/sessions/{id}/send` routes
unchanged. Ids inside payloads are rewritten in both directions: the summary's `id`, `reportTo`, `cleared[].id`;
events carry blob URLs (`/api/blobs/<hash>`), rewritten to `/h/<hostID>/api/blobs/<hash>` (served through the proxy).

### What crosses

| | console → host | host → console |
|---|---|---|
| session list | — | each `/api/hello` / `GET /api/sessions` merges every reachable host's list |
| session ops (`/api/sessions/{id}/…`) | proxied when the id has a host prefix | response ids rewritten |
| `/ws` | `watch` ids split by prefix; per local connection one upstream `/ws` per host | `session`, `events`, `session_removed`, `draft`, `close` rewritten and merged; everything else (layout, config, add-ons, unread, `open`) dropped: those belong to the host's own screen |
| create | `POST /api/sessions` with `host` | the new id comes back prefixed |
| files | `/h/<hostID>/api/fs/*`, `/h/<hostID>/api/blobs/*` | — |

The layout, unread set, config and drafts' storage stay the console's. A host's own UI keeps working on its own: it
just doesn't see the console's layout.

## Trust and credentials

The console authenticates to a host exactly like a paired phone: the host's **remote port** (relay or LAN direct)
with a device cookie. Nothing new is opened on the host, and the rules of docs/dev/REMOTE.md hold: the token is never
accepted remotely, pairing codes are only minted on the host itself.

- Pairing: on the host, Settings → Remote access → "Let another Gerdr control this computer" mints a code marked
  *peer* (`POST /api/remote/pair-code` with `peer: true`, local port only) and shows it as one string,
  `https://<host>/?peer=<code>`. On the console, Settings → Remote access → "Computers this one controls": paste it.
  The console redeems it at `POST /api/remote/pair` with `peer: true` and stores the device token it gets back.
- A peer device is **not temporary** and is **exempt from the passkey re-check** (`reauthNeeded`): a machine can't
  touch Face ID, and a link that drops every day is useless. That is why only a peer code makes one: the code is the
  proof that a person at the host agreed, same as the local "approve App login".
- Where the token lives: `<GERDR_DIR>/peers.json` (0600). **Any agent on the console can read it and so run commands
  on every host.** This can't be engineered away (agents run as the same user); it is stated in the Hosts settings
  page. It is also why trust is one-way: a host never holds a credential for the console, so a prompt-injected agent
  on a host can't climb back.
- Revoking: the host's device list shows the console as a device (label "Gerdr · <console name>"); removing it cuts
  the link. Removing a host on the console deletes the token locally.

## Phases

1. **Link + view** (this branch): `internal/peer` (store, client, upstream ws), peer pairing, merged session list,
   proxied session ops and ws, `/h/<host>/…` proxy, UI: host badge on tabs / rows, host picker in New Session with a
   remote directory browser, Settings → Hosts.
2. Agents across hosts: the built-in gerdr skill learns `--host`; `reportTo` across hosts (the console's reporter
   subscribes to host sessions too).
3. Files: the file viewer opens paths of a host session through the proxy; `gerdr session send --file` copies the
   file into the target host's `~/.gerdr/inbox/<from>/` and rewrites the path in the message.
4. Moving a conversation to another host (handoff across hosts) and artifact folders (shuttle's design:
   docs/collab-local-agent.md in the shuttle repo, folder with an id, pull / push on demand, ETag conflicts, no live sync).

## Not done / known gaps

- **Paths and attachments in a host session still mean this computer.** `@gerdr/chat`'s file hooks (`fileURL`,
  `fileStat`, `openPath`, `upload`) get a path and a cwd, not the session, so a path clicked in a host session's chat
  opens (or fails to open) the console's file, and a non-image attachment is saved on the console where the host's
  agent can't read it. Phase 3: hand the session (or its host) to those hooks, one general change to the package (one
  npm publish), then route through `/h/<host>/api/fs/*`.
- Terminals and the import-from-agent dialog are this computer's only: New Session hides both when another computer
  is picked.
- A /ws connection is linked to the hosts that existed when it opened; the UI reconnects when the set changes
  (`sync.ts` `gotHosts`).
- New Session lists this computer's agents; the host needs the same agent (claude / codex) installed.
- Tried end to end against a real host server over TLS (`hosts_test.go`; and once by hand with a throwaway fake host
  + a console debug instance). Not yet through a real relay and a second real machine.

- LAN direct between console and host (the Android App's trick, dial the LAN address with the host's name as SNI):
  phase 1 goes through the relay address only.
- Android background notices (`/api/app/*`) only cover the console's own sessions.
- A host that is offline: its sessions are absent from the list (tabs showing them keep their last summary and say
  the host is unreachable), not an error.
