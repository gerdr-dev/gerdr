# Local functions (`local/*.ts`)

Deterministic work the pages need goes into a local function: reading a platform's data, running a
CLI and parsing its output, checking a batch of things, writing a report file. gerdr runs it without a
model, so a button press takes seconds, costs nothing and gives the same result every time. **Don't
make the page ask you to run the same commands every time it opens: write the function once, try it,
and let the page call it.**

```ts
// local/mrs.ts → called as "mrs.open"
export async function open(input: { repo?: string }, ctx: any) {
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

| | |
|---|---|
| `await ctx.exec(cmd, [args…], { cwd, input, timeout, env })` | Runs a command-line tool and returns `{ code, stdout, stderr, truncated }`. A non-zero exit isn't an error: look at `code`. Only a missing command, a timeout or a bad `cwd` reject. **The command and its arguments go apart, with no shell in between**: put URLs and user input straight into the array, never into a command string. For pipes and redirects: `ctx.exec('sh', ['-c', 'git log --oneline "$1" \| head -20', '_', branch])`, values passed as `$1`, `$2`; a longer script goes in a file here (`scripts/x.sh`), run as `ctx.exec('sh', ['scripts/x.sh', arg])`. Commands are found on the user's login shell PATH (Homebrew ones too). `cwd` is relative to this directory and can't leave it: for another repo pass its path as an argument (`git -C ~/dev/x`, `glab -R group/repo`). stdin is empty unless you give `input`. `timeout` in ms, 30 s by default, 10 min at most; the whole process tree is killed on timeout or stop. stdout and stderr keep 10MB each. Pass keys through `env: { TOKEN: ctx.secrets.get('TOKEN') }`, not as arguments (those show in the process list). Try the command in your shell first, then put it in the function |
| `await fetch(url, { method, headers, body })` | The global `fetch` (there's no `ctx.fetch`). Returns at the headers: `{ ok, status, statusText, url, headers.get(), body, text(), json(), arrayBuffer(), timing: { ttfb_ms, total_ms }, truncated }`. `body` can be a string or an object (sent as JSON). Bodies up to 10MB: `text()` / `json()` read it all, cut and `truncated: true` past that. **Streams (SSE, slow APIs): read as it comes**: `const reader = res.body.getReader(), dec = new TextDecoder()`, loop `const { done, value } = await reader.read()`, `dec.decode(value, { stream: true })`. Waits 2 minutes for the headers, then gives up after 1 minute without data; no total limit. Local and company-internal addresses are fine |
| `await ctx.fetchAll([url or { url, method, headers, body }, …])` | Fetches 4 at a time and returns the responses in order; a failed one is `{ ok: false, status: 0, url, error }` and doesn't fail the rest. For a batch of pages |
| `ctx.html(text)` | Parses HTML: `.find('css selector')` → `[{ tag, text, attrs }]`, `.text()` the visible text, `.markdown()` the page as Markdown |
| `await ctx.mcp(server, tool, args)` | Calls an MCP tool (the `mcp__<server>__<tool>` you see) and returns `structuredContent`, else the text parsed as JSON, else the text; a tool error rejects. Servers: gerdr's own (Settings → MCP), then Claude Code's (`~/.claude.json`), then Codex's. **A platform's data goes through its MCP when there is one**; wrap it in one file (`local/_okr.ts`) so a change in the tool is one edit. `ctx.mcp.servers()` → `[{ name, status: 'enabled' \| 'disabled', source: 'gerdr' \| 'claude' \| 'codex' }]`: to show a feature only when its server is there, rather than guessing from errors |
| `ctx.secrets.get('NAME')` | A key the user stored with `gerdr work secret set NAME` (the value is read from stdin; the user runs it in a terminal, or you pipe it in when they hand it to you). `null` when there's none: throw an error that says which command to run. Never put keys in the code, the page or files here |
| `ctx.fs` | Files in this Work directory (`ctx.fs.root`), for data a function keeps between runs, exports, reports. Paths are relative with `/`, no absolute paths, no `..`. All synchronous: `list(dir?)` → `[{ path, dir, size, mtime, etag }]` (all levels down, skipping `.` entries and `node_modules`, 5000 at most; a missing dir is `[]`); `stat(path)` → one of those or `null`; `read(path)` → text (10MB at most); `write(path, text, { etag }?)` (creates directories; with the `etag` you read, a file changed since isn't overwritten, it throws) → `{ etag }`; `mkdir(path)`; `move(from, to)`; `copy(from, to)` (both throw if `to` exists); `trash(path)` (into the Trash); `open(path, { reveal })` (opens it with its app / shows it in Finder). `mtime` is in ms. Keep data under a folder of its own (`data/`), not among `ui/` and `local/` |
| `ctx.progress({ done, total, message })` | Progress the page shows while the function runs (and `gerdr work run` prints) |
| `ctx.log(…)` / `console.log` / `.warn` / `.error` | Log lines, shown live and kept in the run log (below). Never log keys, cookies or tokens |
| `await ctx.sleep(ms)` | Waits, 60 s at most per call |
| `ctx.workspace` | `{ project, dir, logs_dir }`: this project's id, this directory, where its run logs are |
| `ctx.locale` | `'zh'`: gerdr's interface language, for messages meant for the user |
| Globals | `fetch`, `URL` (with `searchParams.get/has`), `TextEncoder` / `TextDecoder`, `btoa` / `atob`, `console`, and `Number.prototype.toLocaleString` with thousands separators |

Not here (Annulo has them, gerdr doesn't): `ctx.db`, `ctx.oauth`, `ctx.upload`, `ctx.file`,
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
