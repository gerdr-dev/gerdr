# Events

Things that happen outside a conversation — a review comment, a failed pipeline, a chat message, a mail — as one
record shape, kept by Gerdr and handed to whoever subscribed. Code: `internal/events` (store, patterns, watches),
`internal/work/trigger.go` (triggers/, ctx.events), `internal/server/eventsapi.go` (delivery, HTTP),
`cmd/gerdr/eventcmd.go` (`gerdr event`), add-on side in `internal/addons/service.go` and `ui/src/lib/addons.ts`.

## The record

```json
{ "id": "gitlab:note:123", "source": "gitlab", "kind": "mr.note", "key": "group/repo!34", "actor": "alice",
  "title": "alice commented", "text": "…", "url": "https://…", "data": { … }, "at": 1793700000000, "depth": 0 }
```

- For a conversation (a chat), `key` is **the conversation**: a follow-up carries the same key as what it follows, so
  a subscriber continues by it (the task board sends it to the same task's worker). The source decides what one
  conversation is — the Slack add-on: a DM channel, or a channel thread (a mention that starts one is its root).
- `source/kind` is what patterns match: `gitlab/mr.*`, `*/pipeline.failed`, or a kind alone. No `/` in either, so the
  pattern splits unambiguously. `key` is what it is about; key patterns' `*` crosses slashes (keys are paths).
- `actor` is who did it, in the source's own identities (a Slack member id, a GitLab username); empty when nobody
  did (a pipeline). It's a field, not something in `data`, so a subscriber's gate ("who may ask for work") works the
  same for every source. Identities on different sources are never mapped to each other (dsh-superagent's rule too):
  that one person is `U012` on Slack and `alice` on GitLab is prose the agents read when they need it.
- `id` is the emitter's stable id for the happening. An id seen this month or last is dropped (`fresh: false`), so a
  poller re-finding the same comment every 5 minutes emits it once, and needs no memory of its own for that. The seen
  set is rebuilt from the files on start, so a restart doesn't re-emit. No id = a fresh random one, never deduplicated.
- Kept in `~/.gerdr/events/<yyyy-mm>.jsonl`, append-only, like turns. **Facts, not decisions** (the same rule as
  turns in ADDONS.md): what an event means and what to do is the subscriber's.

## Sources: Gerdr pulls nothing itself

Decided with the user (2026-11): polling, no webhooks in the first version — a webhook needs the computer reachable
(the relay would have to forward it), and a poll survives Gerdr being down (it catches up). Gerdr has no built-in
GitLab / Slack / mail pollers; sources are:

- a Work / panel function on a schedule that polls and `ctx.events.emit`s what it found;
- an add-on service printing `{"type":"event","event":{…}}` (source defaults to the add-on's id) — for a connection that
  must exist once per machine (Slack's Socket Mode), not once per panel;
- an add-on's UI (`g.events.emit`), the CLI (`gerdr event emit`), `POST /api/events`.

Chat / mail / anything with its own credentials is an add-on, not core (the user's call): Gerdr ships the bus.

## Subscribers: every kind of owner can

| who | how | gets |
|---|---|---|
| a panel / Work | `triggers/<id>.json` in its folder, or `user/triggers/` for one person's (`on`, `key`, `fn` or `prompt`, `off`) | `fn` runs with `{event, trigger}`; `prompt` goes to its Work agent |
| (a state, not events) | `gerdr wait <source> <key> --until …`; a trigger with `source`, `key`, `until` | woken once when the condition holds (next section) |
| a Code session | `gerdr event watch --on … [--key …] [--once] [--note …]` | a message per event (title, text, url, the note) |
| an add-on's UI | `g.on("event")`, `g.events.list` | every event, in every open window |
| an add-on's service | addon.json `service.events: ["slack/*"]` | `{"type":"event","event":…}` on stdin |

- Triggers are files, like schedules: the panel owns whether it listens (the file is there) and what it does (its
  function decides, reading its own skills / state). That's the "mechanism in code, the user's way in prose" rule —
  no on/off switch in Gerdr. A trigger's runs are queued and run one at a time, in event order.
- Watches are for sessions, which have no folder: kept in `~/.gerdr/state/event-watches.json` (this machine's). A
  watch whose session is gone is dropped when an event comes for it. `--once` is taken out as it's matched, so a
  burst delivers one.
- `gerdr event list / watch / unwatch / watches` are auto-approved for agents (sysenv.SafeGerdrCLI); `emit` isn't:
  it can start triggers' functions, which is `gerdr panel run` by another door.

## Waiting for a state

Events are for things that **happen**: a DM, a mention. Things that **are** in a state — an MR open or merged, its
pipeline failed, N review comments — are waited on instead: a waiter says the condition, a source reports the
state, Gerdr checks one against the other. Code: `internal/waits` (conditions, latest states, sessions' waits),
`internal/server/waitsapi.go`, triggers with `until` in `internal/work/trigger.go`, `cmd/gerdr/waitcmd.go`.

### Why not events (2026-11-10)

It started with a Code session's `gerdr event watch --on 'gitlab/*' --key 'craft/portai!4075'`, taken and never
fired: the only GitLab poller was the superagent panel's `mrwatch`, polling its own tasks' MRs. The first fix let
subscriptions tell sources what to poll. Then the user asked what happens when an agent opens an MR, forgets to
watch, and is reminded five minutes later: the comment made meanwhile was said once, to nobody, and is gone. Every
fix for that is a patch on the event — dedup, replay from the log, a "first look" that re-tells history, `final` to
end watches — because an event is said once, can't be repeated and can't be asked again. The user's call: let the
waiter state what it expects, and wake it when the actual state differs. A condition checked against the state as
it is gives the same answer every time it is checked, so it can't be missed: a wait written late on something
already true wakes at once. The expectation is always the waiter's own.

### The three parts

| part | knows | does |
|---|---|---|
| **waiter** — a session (`gerdr wait`), a panel (a trigger with `until`) | which thing (source + key) and the condition | waits; is woken once with the state |
| **Gerdr** | every wait; the latest state of each thing waited on; when to ask next | asks the source about each thing waited on; checks every wait whenever an answer comes in or a wait is added |
| **source** — an add-on whose addon.json has `"states"` | how to look at things outside (glab, a token) | answers "what is this thing like now?", or why it can't |

A source is a function from a key to a state: it keeps no list of keys, no timer, no conditions, no memory of what
it said (caches aside). It never sees who waits — no sessions, panels or projects. A waiter never names an add-on,
only the source and the key. Gerdr holds no knowledge of either side: no GitLab in Gerdr, no "which MRs matter" in
the add-on.

### The flow

```
 waiter                              Gerdr                                         source (add-on service)
 ──────                              ─────                                         ───────────────────────
 gerdr wait gitlab K --until C ───▶ wait saved (state/waits.json)
 ctx.fs.write user/triggers/x.json ▶ trigger with until (Work.Changed)
                                     │ the poller: every thing waited on, asked at once
                                     │ when new, then every poll interval
                                     ├── {"type":"get","id":"r7","key":K} ───────────────▶ looks at K
                                     │◀─ {"type":"state","id":"r7","state":{…},"detail"} ─┤
                                     │   or {"type":"error","id":"r7","reason","retry"} ──┤
                                     │ keep it (state/source-states.json); for every wait on K: does C hold?
 session: a message ◀────────────────┤  yes → a session's wait is woken and gone;
 panel: its fn runs ◀────────────────┘        a panel's trigger runs once (spent until C stops holding / the file changes)
```

- **Asking is Gerdr's** (`poller` in waitsapi.go): one record per thing waited on — when it was last asked, the
  answer (a state, or an error and whether it passes by itself), when to ask next. A new wait (a session's, or a
  panel's trigger file written with `ctx.fs` — `Work.Changed` — or changed any other way: every 2s the waited-on set
  is recomputed, cheap) is asked about at once; then every `states[].poll` seconds (default 60, at least 10). An
  error with `retry: true` (the network) is asked again in 20s, one without (not logged in, no such MR) every
  interval, shown with the wait meanwhile. At most 4 asks at once. A thing waited on again within 10 minutes of the
  last wait keeps its last answer for display and is asked at once.
- **Checked when**: whenever an answer comes in, and when a wait is added — a session's at once against the last
  known state, then again when the first answer comes (the request waits up to 10s, so `gerdr wait` says straight
  away whether it already held, or what the state is, or why the source can't look); a panel's when its trigger file
  is written, and on every answer.
- **Woken once.** A session's wait is removed when woken; a panel's trigger is spent until its condition stops
  holding or its file changes (usually its function trashes it or rewrites it). The wake message carries the state's
  values; the waiter states its next expectation from them. Counts are written as "more than now" (`reviews > 3`):
  `reviews > 0` again right after handling them would wake at once.
- **A source may also report unasked** (`{"type":"state"}` without an id, naming the key) when it learns of a change
  by itself (a webhook, a socket); Gerdr checks the waits the same way. Not needed by polling sources.
- **Conditions are one JavaScript expression** over the state (`waits.Cond`, goja): each field is a variable, `$`
  the whole state. Evaluated by Gerdr, so every source gets them for free and only reports data, lists included
  (the GitLab add-on reports `notes`, so `notes.some(n => n.author != me && !n.resolved && n.path == 'a.go')` works
  without a field for it). For a day it was a little language of Gerdr's own (`field op value`, `||`, `&&`); the
  user asked for JS when filtering lists came up — every filter would otherwise have needed its own field. Sandbox:
  a bare goja runtime (no require, files or network), the expression wrapped so it can't be statements, 50ms per
  check (`vm.Interrupt`). A misspelt field is a ReferenceError: refused when the wait is made if the state is known
  (or as soon as the first one comes in), shown with the wait otherwise — never a silent "false".
- **Polling sees moments.** A pipeline that failed and was retried green between two looks is seen as green. A
  waiter that cares writes `pipeline_id != <now>` ("a new pipeline"); that is polling's limit, not the model's.
- **Keys carry what the source needs.** GitLab keys are `<host>/<project path>!<iid>`, as in the MR's address: one
  add-on serves every GitLab with glab's own logins, two GitLabs' `group/repo!34` never mix, and projects never reach
  the source (the user has projects on different GitLabs and only some want MR news: that is just who waits on what).

### Rejected on the way

- **The add-on maps Gerdr projects to GitLab hosts.** A project is Gerdr's concept; the host is in the key instead.
- **Events for MRs, with subscriptions telling the poller what to poll** (`service.subscriptions`, a
  `ctx.events.follow` call, triggers as subscriptions, `final`): built, then replaced — late or missed subscribers
  lose what was said, see above.
- **The add-on picks its own scope** ("every MR I created"): it would be its guess, not what anyone asked for.
- **Watching adds the repository to an add-on setting**: a lossy channel with two writers.
- **The source polls by itself, told the keys waited on** (`{"type":"subscriptions"}` with the whole list, answered
  with `{"type":"serving"}`): a leftover of the event design, replaced the same day by Gerdr asking — the source then
  needed a list, a timer, first-look and retry logic of its own, and Gerdr a list to diff and resend; with `get` the
  source is a function and when-to-ask lives in one place.
- **Standing waits that wake on every match**: they need the same baselines and turn back into "push every change".

## Offering sources and answering, without naming them

Many users have no Slack; others have Feishu. So a panel never lists sources in code:

- An add-on declares what it emits in addon.json `emits` (`kind`, `title`, `description`, `reply`).
  `ctx.events.catalog()` / `GET /api/events/catalog` merges the switched-on add-ons' declarations with source/kinds
  seen in the last 30 days (a panel's own poller needs no declaration to show up), minus answers (kinds some event
  names as its reply). The task board's "触发器" settings page is a list of that.
- An event carries its own reply address, `reply: {kind, data}`, filled by the source. `ctx.events.reply(event, text)`
  emits `{kind: reply.kind, data: reply.data + text}` under the panel's name, and the source, subscribed to that kind,
  posts it. Slack and Feishu answer the same way for the panel; only the add-on knows channels and threads.

An agent's approvals and questions can be answered from a chat the same way, without the chat knowing: the
`remote-answer` add-on gets them from Gerdr (a service with `"pending": true`, docs/dev/ADDONS.md), posts through the
reply address of the person's last `dm`, and reads the answer from that conversation's next `dm` events.

## Loops

A trigger's function may emit events (a poller fanning out, a reply). Each run knows the depth of the event that
started it (`depthOf(input)`), and what it emits is one deeper. Events deeper than `events.MaxDepth` (4) are kept
and broadcast but not delivered to triggers, watches or services, so two triggers feeding each other stop after a
few rounds instead of spinning forever.

## Add-on services get their settings

A service can't call `g.settings.get` (it isn't a page), and a source needs its token. So right after the greeting a
service gets `{"type":"settings","settings":{…}}` (defaults overlaid with what was set, secrets in the clear, links
left out), and again whenever they change (Services.Sync runs on every change of the add-on directory or state file).

## Not done yet

- A page for triggers (last runs, errors) next to the schedules in a panel's settings; `gerdr panel triggers` has it.
  Trigger runs' last state is kept in memory only.
- Webhooks in, through the relay, behind the same auth as `/api/`.
- Pruning old month files (they're small: a busy poller is a few MB a month).
