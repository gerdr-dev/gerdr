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
Work directory), `sessions` (hand a task to a coding agent and follow it), `workspaces` (the Code
side's workspaces), `dirs` (directories anywhere on disk, read-only), `progress`, `log`, `sleep`.

Globals: `fetch(url, { method, headers, body })` (there's no `ctx.fetch`; `body` can be an object, sent
as JSON; bodies up to 10 MB; waits 2 minutes for the headers, then gives up after 1 minute without data; for
streams read `res.body.getReader()` as it comes; local and company-internal addresses are fine), `URL`,
`TextEncoder` / `TextDecoder`, `btoa` / `atob`, `console`, and `Number.prototype.toLocaleString` with
thousands separators.

Not here (Annulo has them, Gerdr doesn't): `ctx.db`, `ctx.oauth`, `ctx.upload`, `ctx.file`,
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
gerdr work run mrs.open '{"repo":"group/app"}'     # progress and logs on stderr, the result on stdout
gerdr work run mrs.open --input-file input.json   # or - for stdin
gerdr work functions                              # what can be called
```

A thrown error prints its stack (file and line, counted in the bundled code: imported helper files
come first) and exits 1. Ctrl-C stops the run. Fix it until it runs, then wire the page.

## Run logs

Every run's progress, logs and error (with its stack) are kept 30 days in `ctx.workspace.logs_dir`;
the input, the returned value and keys aren't.

- `gerdr work logs --fn mrs.open -n 5`: the latest runs (id, status, time, error message).
- `gerdr work logs --id <run id>`: one run's full log.
- `gerdr work abort <run id>`: stop a run still going.
- When something timed out, don't just do it again: check first (read-only) whether it went through.
