# Local functions (`local/*.ts`)

Deterministic work the pages need goes into a local function: reading a platform's data, running a
CLI and parsing its output, checking a batch of things, writing a report file. Gerdr runs it without a
model, so a button press takes seconds, costs nothing and gives the same result every time. **Don't
make the page ask you to run the same commands every time it opens: write the function once, try it,
and let the page call it.**

```ts
// local/mrs.ts → called as "mrs.open"
export async function open(input: { repo?: string }, ctx: Ctx) {
  const args = ['mr', 'list', '-F', 'json', '--per-page', '50']
  if (input.repo) args.push('-R', input.repo)
  const r = await ctx.exec('glab', args, { timeout: 60_000 })
  if (r.code !== 0) throw new Error('glab failed: ' + r.stderr.trim())
  const list = JSON.parse(r.stdout)
  ctx.progress({ done: list.length, message: 'loaded' })
  return list.map((m: any) => ({ id: m.iid, title: m.title, draft: m.draft, url: m.web_url }))
}
```

- `export [async] function name(input, ctx)`; the call name is file + export (`local/mrs.ts`'s `open`
  is `mrs.open`). Files starting with `_` (`local/_glab.ts`) hold shared code: import them by relative
  path; they can't be called. No npm packages, no `require`.
- What you return must be JSON-able; it goes to the page as is.
- A thrown error's message is shown to the user as is: write it so they can act on it ("glab isn't
  signed in: run `glab auth login`").
- There's no event loop: no `setTimeout`. Every ctx call finishes before the next line (it returns a
  settled promise, `await` works as usual); to wait, `await ctx.sleep(ms)`.
- A run takes up to 60 minutes; it doesn't stop when the page closes, only when stopped.

## What a function can use

**`ctx` is described in `local/ctx.d.ts`**: every member with its arguments, what it returns and its
limits. Read it before writing a function and type the argument `ctx: Ctx` (no import needed). In short:
`exec` (command-line tools), `mcp` (MCP tools), `fetchAll`, `html`, `secrets`, `fs` (files in this
panel directory), `sessions` (hand a task to a coding agent and follow it), `workspaces` (the Code
side's workspaces), `dirs` (directories anywhere on disk, read-only), `inbox`, `progress`, `log`, `sleep`.

What on this panel needs the user goes to Gerdr's inbox with `ctx.inbox.set([{ id, title, body?, session? }])`: on
every device, and as a notification on the phone App. Pass the whole current list each time (from wherever the panel
works out what waits on the user, e.g. after each change and on a schedule): an item left out is done and leaves the
inbox, the same `id` again is the same item. `session` makes the item open that conversation.

Images and files the user gives a task (pasted, dropped, picked on a page): the page POSTs each to
`/api/fs/upload?name=<file name>` (same origin, body = the file, header `X-Gerdr: 1`) and gets `{ path }`; pass the
paths as `attachments` to `ctx.sessions.start` / `send`, and the agent gets them as from Gerdr's own input box.

Handing work out without waiting on it: `ctx.sessions.start({ ..., tags, reportTo: "work" })` returns at once;
each time that worker stops (done, waiting for a person, failed) the panel's agent gets a report with its reply (not
after a turn the user drove in the worker's own chat), so
nothing has to poll. `tags` let a later run find "the session for this task / MR" again (`list({ tags })`), and
`close(id)` parks a worker whose part is done (a later `send` opens it again with its context). Keep tag values
to letters, digits and `/ - _ . :`: the panel's agent types them into `gerdr session` commands, where an unquoted `!`
or `#` makes the command wait for the user's approval.

Globals: `fetch(url, { method, headers, body })` (there's no `ctx.fetch`; `body` can be an object, sent
as JSON; bodies up to 10 MB; waits 2 minutes for the headers, then gives up after 1 minute without data; for
streams read `res.body.getReader()` as it comes; local and company-internal addresses are fine), `URL`,
`TextEncoder` / `TextDecoder`, `btoa` / `atob`, `console`, and `Number.prototype.toLocaleString` with
thousands separators.

Not available in Gerdr: `ctx.db`, `ctx.oauth`, `ctx.upload`, `ctx.file`,
`ctx.folders`, `ctx.browser`, `ctx.llm`, `ctx.agent`, `ctx.user`, `ctx.chat_id`. Keep data in files
(`ctx.fs`) or the platform it comes from.

## Calling it from a page

```tsx
import { run, abort, RunError } from "@/lib/gerdr";

const [progress, setProgress] = useState<any>();
const list = await run("mrs.open", { repo }, { onProgress: setProgress, onLog: (l) => console.log(l.message) });
```

`run()` resolves with what the function returned and rejects with a `RunError` (`message`, `stack`,
`runId`). Pass `signal` (an `AbortController`'s) to stop it, or `onStart(id)` + `abort(id)`.
`attach(id)` follows a run again after a reload. `functions()` lists what can be called. Show
progress for anything that can take more than a second or two, and a way to stop long runs.

## Try it before handing it over

```
gerdr panel run mrs.open '{"repo":"group/app"}'     # progress and logs on stderr, the result on stdout
gerdr panel run mrs.open --input-file input.json   # or - for stdin
gerdr panel functions                              # what can be called
```

A thrown error prints its stack (file and line, counted in the bundled code: imported helper files
come first) and exits 1. Ctrl-C stops the run. Fix it until it runs, then wire the page.

## As tools for agents

A local file can offer some of its functions as tools to the agents this panel works with (you, and every session
the panel starts): they get them over MCP as `work`, can call them whenever they need, and get the result back in
the same turn. That's how an agent reports to and changes something of yours (a task board: "I'm done", "I need the
user to sign in", "never mind, they answered") instead of writing text you'd have to parse.

```ts
export const tools = {
  ask_human: { description: "Ask the user to do a step only they can do…", input: { type: "object", properties: { title: { type: "string" } }, required: ["title"] } },
};
export async function ask_human(input: { title: string }, ctx: Ctx) {
  const who = ctx.caller.session; // the conversation that called it: which task's worker it is
  …
}
```

- The key is the tool's name and runs the export of the same name; `input` is its JSON Schema. A description says
  when to call it and what it does: it's all the agent knows about it.
- `ctx.caller.session` is the calling conversation's id: over MCP, or `gerdr panel run` run by an agent (which then
  runs in that conversation's project, wherever its cwd is); "" for a page or a person's terminal. Don't trust input for
  "who am I": look the caller up.
- A thrown error goes back to the agent as the tool's error: say what to do instead.
- A result the user should see in the chat too (a step put on their board): return it with
  `gerdrCard: { title, body, tone: "warn" | "info" | "ok" }` (body is Markdown). Gerdr draws that call as a card
  instead of the folded "called a tool" row; the rest of the result still goes to the agent. `ref` names it (a page's
  showSession button with `jump: ref` scrolls the chat there). `choose: { project, fn, input, field, options, other }`
  makes it answerable in place: the user's pick runs that local function with `{ ...input, [field]: pick }` — the
  same call your page makes, so answering in the chat or on the page is one thing. `multi: true` lets the user tick
  several options and submit them together; the pick is then the ticked ones (and anything written), one per line. `type: "<name>"` has the card drawn
  by the component the page registered for that name in `ui/src/cards.tsx` (`registerCard` from `@gerdr/core`), with
  every other field of the card as its data; keep `title` / `body` filled in, they're drawn when the type isn't.
- Sessions get the tools when they start or are reopened; the list is read again each time an agent asks for it.
- `export function instructions(input, ctx)` in the same file: what you tell each of your conversations about itself
  (which task it works on, where its rules are), as text; "" for none. It goes with the tools as the MCP server's
  instructions, which stay in the agent's system prompt: a brief in its first message is gone after `/clear`, this
  isn't. Run for that conversation (`ctx.caller.session`) when its agent starts.
- A conversation is yours when you started it (`ctx.sessions.start`) or it carries your `panel` tag: it gets your
  tools and skills the next time its agent starts.

## On a schedule

What the project does by itself goes in `schedules/<id>.json`, one file per job (the file name is its id). Gerdr's
backend runs them while it's up: with the background service on, the App needn't be open, nor this page.

```json
{ "fn": "mrwatch.tick", "every": "5m", "name": "Watch my MRs" }
{ "fn": "report.collect", "at": "09:00,16:00", "input": { "days": 1 } }
{ "prompt": "Sum up yesterday's merged MRs for the standup", "at": "09:30", "name": "Standup notes" }
```

- `fn` runs a local function, with `input` plus `scheduled: true`; `prompt` sends the text to you (the panel's agent),
  as if the user had. One of the two. Deterministic work is a function; only what needs judgement is a prompt, and a
  watcher is best a function that polls and filters and hands just the interesting part to an agent
  (`ctx.sessions.send` to the worker concerned, or to you with `"work"`).
- `every`: 30s, 30m, 6h, 1d (at least 10s); `at`: local times of day, comma separated. Missed runs (asleep, Gerdr off) are
  caught up once; a run still going when its time comes again is skipped. `user/schedules/` are this person's own (AGENTS.md, `user/`). A run is an ordinary run in the run logs.
- A broken file stops the whole folder (the error is in `gerdr panel schedules`): fix it rather than delete others.
- `gerdr panel schedules` lists them with the last run (result, error, run id) and the next time;
  `gerdr panel schedule run|pause|resume <id>`. Paused / last run are this machine's, not in this directory.
- A function on a schedule keeps its own memory in `user/data/` (what it has already handled), so the same comment or
  failure isn't handed over twice.

## On an event

Things that happen outside a conversation — a review comment, a failed pipeline, a chat message — are **events**:
`{ id, source, kind, key, title, text, url, data }`. Where they come from: a function on a schedule that polls and
`ctx.events.emit`s what it found, an add-on (a Slack listener), or `gerdr event emit`. Giving `id` a stable value
(`gitlab:note:123`) makes finding the same thing again a no-op, so a poller needs no memory of its own for that.

What this project does when one comes goes in `triggers/<id>.json`, the twin of `schedules/` (one the user turned on
for themselves, e.g. from a settings page: `user/triggers/<id>.json`, AGENTS.md `user/`):

```json
{ "on": "gitlab/mr.*", "fn": "mrwatch.onEvent", "name": "MR news to its worker" }
{ "on": "slack/message", "key": "C0123ABC", "fn": "inbox.onSlack" }
{ "on": "grafana/alert.firing", "prompt": "Look into this alert and tell me what you find" }
```

- `on`: `source/kind` with `*` (`gitlab/mr.*`, `*/pipeline.failed`), or a kind alone for any source; `key` narrows it
  to one thing (`*` crosses slashes: `group/repo!*`).
- `fn` runs a local function with `{ event, trigger }` (plus `input`); `prompt` sends you the event and the text.
  Whether to act at all is the function's call: read your skills' settings, the board's state, then do or don't.
  A trigger's runs go one after another, in the order the events came.
- Offering events to subscribe to (a settings page's "which events make a task"): `ctx.events.catalog()` — what the
  installed add-ons emit and what came lately. Don't name sources in code: a user without the Slack add-on has no Slack.
- `"off": true` keeps a trigger file but stops it. Switching off one the panel ships is a copy in `user/triggers/`
  with `"off": true`.
- Answering: `ctx.events.reply(event, text)` goes back where the event came from (a chat's thread), whatever the
  source; an event without `reply` has nowhere to answer.
- A function run by a trigger may emit events itself; they're one level deeper, and past 4 levels they aren't handed
  on, so two triggers feeding each other stop.
- `gerdr panel triggers` lists them with how many runs and the last one; `gerdr event list --on 'gitlab/*'` shows what
  came. A broken file stops the whole folder, like schedules.

## Waiting for a state

Some things are in a state rather than sending events: an MR is open or merged, its pipeline running or failed, it
has some review comments. An add-on with `"states"` reports them (the GitLab one: MRs). Wait on one with a trigger
that has `source`, `key` and `until` instead of `on`:

```json
user/triggers/mr-34.json
{ "source": "gitlab", "key": "gitlab.example.com/group/repo!34",
  "until": "state != 'opened' || pipeline == 'failed' || reviews > 3", "fn": "waits.onState" }
```

- `fn` runs with `{ state, detail, url, source, key, until, trigger }` once the condition holds for the latest
  state — at once if it already does, so a wait written late misses nothing. It runs once; it runs again only after
  the condition stopped holding, or the file changed. Usually the function handles it and trashes the file, or
  rewrites it with the next expectation.
- `until` is one JavaScript expression over the state: each field is a variable (`$` is the whole state), strings in
  quotes; lists filter like any JS (`notes.some(n => n.author != me && !n.resolved)`). It reads only the state, 50ms
  per check. Write counts as "more than now": read `ctx.states.get(source, key)` and use `reviews > <now>`.
- Writing the file with `ctx.fs` makes Gerdr ask the add-on about it at once (it is asked only about what is waited on); trashing it stops that.
  `gerdr panel triggers` shows each one's state now, whether it holds, and whether the add-on took the key.
- `gerdr wait sources` lists what can be waited on, how keys look and what each field means.

## On a live connection

A service that says the moment something changes (a WebSocket), instead of being polled: declare it in
`streams/<id>.json`, and Gerdr's backend keeps the connection open for this panel (your functions only run and end).

```json
{ "connect": "team.stream", "on": "team.tick", "name": "Team server" }
```

- `connect` is a local function returning `{ url: "wss://…", headers?: { … } }`, or `null` for not now (not signed in
  yet: asked again every 30 s). It's called before every connect and reconnect, so a new token or address is picked up.
- `on` runs with `{ message, stream }` for what comes (`message`: the JSON value, or the text). Messages that come while
  it runs collapse into one more run with the latest, so make it a catch-up ("fetch what changed since what I have"),
  not a handler of each message. Nothing is queued while the connection is down: keep a schedule as the backstop.
- A dropped connection is dialled again, with backoff up to a minute. `gerdr panel streams` shows each one's state,
  error, messages and runs. `user/streams/` are this person's own, like schedules.

## Run logs

Every run's progress, logs and error (with its stack) are kept 30 days in `ctx.workspace.logs_dir`;
the input, the returned value and keys aren't.

- `gerdr panel logs --fn mrs.open -n 5`: the latest runs (id, status, time, error message).
- `gerdr panel logs --id <run id>`: one run's full log.
- `gerdr panel abort <run id>`: stop a run still going.
- When something timed out, don't just do it again: check first (read-only) whether it went through.
