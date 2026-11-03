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

1. **Link + view** (done): `internal/peer` (store, client, upstream ws), peer pairing, merged session list,
   proxied session ops and ws, `/h/<host>/…` proxy, UI: host badge on tabs / rows, host picker in New Session with a
   remote directory browser, Settings → Remote access → "Computers this one controls".
2. **Agents across hosts** (done), see "Agents and reports across computers" below.
3. **Files** (done), see "Files" below.
4. Not done: moving a conversation to another host (handoff across hosts) and artifact folders (shuttle's design:
   docs/collab-local-agent.md in the shuttle repo, folder with an id, pull / push on demand, ETag conflicts, no live
   sync). `send --file` / `fetch` cover handing single files over meanwhile.

## Agents and reports across computers

`gerdr session …` on the console already sees host sessions (they are in `/api/hello`); what needed care:

- `gerdr session hosts` lists the paired computers; `new --host <id>` starts one there. Its `--cwd` is resolved on that
  computer (`~` = its home, asked with `/api/fs/dirs?host=`), never with this one's working directory. The skill says
  to quote `'~/…'`: unquoted, the agent's shell expands it to *this* home (and an unquoted `~` makes the command need
  approval, `sysenv.SafeGerdrCLI`).
- `open` / `close` / `meta` on a host session are answered by the console (`hosts.go` `hostOpen` / `hostClose` /
  `hostMeta`), not proxied: the screen a person looks at is the console's, and so is a report target that isn't on the
  host. Closing also shuts the agent down over there (`/shutdown`), like closing a pane.
- **Who sent it** (`from`): a message from a console session to a host session carries `from: "console~<id>"`
  (`peer.RewriteBody`); the host stores it as is, and `peer.RewriteEvents` strips the mark when the event comes back,
  so "from session X" still points at X here. Dropping it instead would make the host's hub take the message for a
  person's (`Summary.ByPerson`: no `from`), and then that turn would never be reported. Senders on the host itself
  get the host prefix the same way.
- **Report targets**: a host session reporting to a session on the same host is the host's own (`Record.ReportTo`,
  its reporter). One reporting to a console session, or to another host's, can't be stored there (the host can reach
  nothing but itself), so the console keeps it in `<GERDR_DIR>/peer-reports.json` (`peer.Manager.ReportTo`), shows it
  on the summary, and setting one clears the other. The console's reporter (`report.go`) subscribes to every host
  (`peer.Manager.Subscribe`, one upstream per host, following Add / Remove, running with no window open), reads a
  worker on another computer through `source` (summary + events over HTTP) and delivers to a target there through
  `peer.Manager.Send`. A console session may report to a host session too: `Hub.Elsewhere` lets the hub accept a
  target it doesn't hold and keep walking for loops through it.

## Files

- `@gerdr/chat` got one general hook, `ChatHost.forSession(sessionId)`: overrides of any host member for one
  session. ChatView provides the merged host (`SessionHost`) to everything below it, and the package's code outside
  React that handles a session's files uses `hostFor(sessionId)` (media URLs, path links, bare-file-name checks keyed
  by session as well as cwd, the lightbox's "open in viewer", transcript video links). gerdr's `forSession` points a
  host session's `fileURL` / `fileStat` / `fileExists` / `openPath` / `upload` at that computer and removes
  `localPath` / `openNative` (a file picked here has no path there; attachments get uploaded to it). **This changed
  the package: other apps get it with the next npm publish** (nothing for them changes unless they give `forSession`).
- The viewer and file panes carry the host (`FileStat.host`, `PaneLeaf.file.host`, `?host=` / `/h/<host>/` URLs).
  "Open in the system browser" uses a `/f/` link issued by that computer and opened at its own address: the console
  can't serve a cookie-less link for another machine.
- The proxy passes file contents through untouched in both directions (`/api/fs/upload`, `/api/fs/raw`, `/f/`):
  an uploaded or viewed `.json` file is not an API payload to rewrite. Uploads stream; the peer client has no overall
  timeout (a proxied video plays as long as it plays), each request carries its own context.
- Agents: `gerdr session send <session> "…" --file <path>` (repeatable) attaches files as resource links; for a
  session on another computer each is uploaded there first (that computer's `<GERDR_DIR>/uploads/`) and the link is
  the path there. `gerdr session fetch <session> <path> [dest]` copies a file from the computer a session runs on to
  here (relative to that session's directory). Folders aren't taken: pack them first.

## Not done / known gaps

- Terminals and the import-from-agent dialog are this computer's only: New Session hides both when another computer
  is picked.
- A /ws connection is linked to the hosts that existed when it opened; the UI reconnects when the set changes
  (`sync.ts` `gotHosts`).
- New Session lists this computer's agents; the host needs the same agent (claude / codex) installed.
- Loop checks across computers only see one step through the console (a target here that reports straight back);
  longer loops through two hosts aren't caught.
- Tried end to end against a real host server over TLS (`hosts_test.go`; and by hand with a throwaway fake host + a
  console debug instance: pairing, sessions, the New Session picker, the CLI's hosts / list / fetch, a path and an
  image of a host session opening through `/h/`). Not yet through a real relay and a second real machine.
- LAN direct between console and host (the Android App's trick, dial the LAN address with the host's name as SNI):
  only the relay address for now (`peer.Manager.Route` is where it would go).
- Android background notices (`/api/app/*`) only cover the console's own sessions.
- A host that is offline: its sessions are absent from the list (tabs showing them stay, waiting), not an error.
