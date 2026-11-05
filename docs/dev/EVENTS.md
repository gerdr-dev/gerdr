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
| a panel / Work | `triggers/<id>.json` in its folder, or `user/triggers/` for one person's (`on`, `key`, `fn` or `prompt`) | `fn` runs with `{event, trigger}`; `prompt` goes to its Work agent |
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
