# Artifacts: folders and records a piece of work leaves behind

Status: agreed 2026-11-12. Built: the core and panels (see "As built"); the Superagent panel uses them (its DESIGN.md
"任务文件夹"). **Code has none since 2026-11-13** (below): an artifacts folder is a board's capability, for its tasks.

## Code keeps none (2026-11-13)

Built first for Code too (one folder per repository, `~/Gerdr/<project>/<repo>/`, named in every session's prompt,
a folder button on every session pane). The user: "我希望code模式下不要有任何我们新加的文件夹产物的逻辑，我希望他保持原样。
文件夹产物应该是看板……的一个能力。" So a session has an artifacts folder only when whoever started it gave one
(`ctx.sessions.start({ artifactsDir })`: a panel's task); no prompt section, no pane button, and `gerdr artifacts`
says there is none otherwise. `Store.ForRepo` went with it; the parts below about Code are history.

## Why

The user does real work through Gerdr that ends in things, not in code: an article ends in drafts, copy per platform,
images and videos; a Superagent task ends in a PR, a demo folder somewhere, a test account. Today those things are
scattered: files wherever the agent wrote them (sometimes inside a repo where they don't belong), links only in a
Superagent task's `links`, everything else only in some conversation. Nobody can find them a week later.

The model is the user's own Annulo (shuttle) creator template, which they use daily for self-media
(`annulo-template-public/creator`, `docs/content-model.md`): one content item = one real folder
(`~/Annulo/自媒体/内容/<title>/`), files on disk are the truth, conventions (`稿子.md`, `*文案*.md` with `title` / `tags`
frontmatter, one directory per version, scratch in `草稿/`) live in the task's prose, and the page shows the folder as a
tree with preview and Markdown editing. What made it work: the files are the result (Finder, the video editor and
the page all see the same file), the folder has an id so renames don't break it, and it can be seen and edited in place.

## Not every piece of work has artifacts

A development task usually ends in commits: the code, and the docs that belong with it, go in the repository and are
committed. **An artifacts folder is for what shouldn't be committed but someone needs to see or keep**: generated
images and videos, reports, drafts, exported data, copy to publish. So folders are created lazily, the first time
something is put in one, and agents are told plainly: things that belong in the repo go in the repo; don't create an
artifacts folder unless there is such output.

## What an artifact is

| Kind | Example | Where it lives |
|---|---|---|
| file | draft, image, video, report | in the folder, as a plain file (not recorded anywhere else) |
| link | PR / MR, pipeline, published post, external doc | a record in the folder's list |
| path | "the new project is at `~/dev/xxx`", a folder created elsewhere | a record (points there, never copied) |
| text | a test account, the one-line pitch, a command to remember, a conclusion | a record if short; a `.md` file if longer than a few lines |
| pinned file | the final video among twenty drafts | a record pointing at a file in the folder |

## On disk

```
~/Gerdr/                              root (setting; default ~/Gerdr, visible in Finder on purpose)
  .gerdr/index.json                   project id → folder name (kept when the project is renamed), folder id → path,
                                      owner → folder id
  <project name>/                     one per Gerdr project, named after its display name ("my", "lb")
    <repo>/                           Code: one per repository
    任务/<task title>/                 Superagent: one per task (the panel chooses `under`)
      .gerdr/folder.json              {id, owner, created_at} — owner: {repo: <git common dir>} or {panel, task}
      .gerdr/items.jsonl              the records, one JSON per line, append-only
      .gerdrignore                    what isn't synced (same file `gerdr folder` already reads); default 草稿/ drafts/ cache/ *.tmp
      稿子.md …                        the files
```

- Not under `~/.gerdr`: people drag these into editors, send them, open them in Finder.
- **Identity is `folder.json`'s id**, not the path. The user may rename or move a folder in Finder; Gerdr finds it again
  by id (the index's folder id → path, repaired by a scan when the folder isn't where it was), like Annulo's
  `.annulo/folder.json` + `folders.json`.
- **Code: one folder per repository**, keyed by the repository's git common dir, so every worktree and every session
  of weave shares `~/Gerdr/my/weave/`. Agents make a subfolder per thing (`首页配图/`, `perf report 11-12/`). A cwd that is
  not in a repository uses its own directory name.
- Records (`items.jsonl`): `{id, kind: link|path|text|file, label, url|path|text, at, session, superseded?}`. Append-only,
  the same rule Superagent's `links` already follow (the user wants stale ones kept to trace back); an outdated record
  gets a later `{supersedes: id}` line and is drawn greyed, never deleted. A record is created in the folder, so
  recording the first link creates the folder too (it then shows records and no files; that's fine).

## Who knows which folder

- A session has an **`artifactsDir`**: given explicitly when started (`ctx.sessions.start({artifactsDir})`, `gerdr session new
  --artifacts`), else derived: `~/Gerdr/<project>/<repo>/` from its cwd. Nothing is created until something is written.
- The system prompt gets a short section next to the project memory (`agentwire.SessionPrompt`): the path, what goes
  there and what goes in the repo, and `gerdr artifacts` for records. Per session, so it can name the exact folder.
- **CLI `gerdr artifacts`** (the caller's session is resolved by the server, like `gerdr session`; `--dir` to name another
  folder):
  - `gerdr artifacts` → prints the folder (creating nothing)
  - `gerdr artifacts link <url> [--label]`, `gerdr artifacts path <path> [--label]`, `gerdr artifacts text <text> [--label]`,
    `gerdr artifacts pin <file>`; `--supersedes <id>`
  - `gerdr artifacts ls` → records and files
  - The built-in gerdr skill documents it; its description mentions artifacts / outputs so it is read.
- Panels: `ctx.folders.create({name, under, owner}) → {id, path}`, `resolve(id)`, `move(id, newName)` (keeps the id),
  `items(id)`, `record(id, item)`; `ctx.fs` may address the artifacts root, not only the panel directory. Typed in
  `ctx.d.ts`.

## UI: one component, used everywhere

`<Folder path>` in `@gerdr/core` (panels) and in the app (Code):

- **Records** strip on top: links as cards (an MR / PR shows its state through the existing waits / MR add-on), paths
  as rows (browse beside, reveal, open a session there), text as copyable blocks, pinned files as thumbnails.
- **Files** below: tree + list / grid, preview in place reusing FileViewer's renderers (image, video with Range, audio,
  PDF, text). Toolbar: new file, new folder, upload / drop, reveal in Finder, refresh; rename, move, delete (to trash).
- **Markdown is editable in place**, like Annulo (rich text with a source toggle; tiptap + @tiptap/markdown, with
  Annulo's `mdExtensions` tweaks so reading and saving back doesn't rewrite the file: compact tables etc.). Saves use
  the existing `PUT /api/fs/file?ifMatch=`; when the agent changed the file meanwhile: unedited → reload silently,
  edited → "load the new version / keep mine". Other text stays read-only.
- Live: re-listed every 3 s while the page is visible (the root, open folders, the selected one: one level each).
  A server-side watch pushed over /ws would be cheaper; not worth a dependency until the polling shows up somewhere.

Where it shows:
- **Code**: a "Files" button on the workspace / tab opens the repository's artifacts folder in a pane beside the chat
  (the ⌥-click file pane mechanism). Later the same component pointed at the cwd gives a code tree: two tabs,
  "Artifacts" and "Code".
- **Superagent**: the task detail gets a "Files" tab next to the conversation; the card shows a summary
  ("PR !34 merged · 3 files"). `links` move into the folder's records (migrated once); attachments the user uploads are
  copied into `素材/` and the worker is pointed there.

## Backend

On top of the existing `/api/fs/*` (`stat`, `raw`, `tree`, `file`, `upload`, `mkdir`):
- `GET /api/fs/list?path=` one level, ignores applied; `POST /api/fs/move`, `POST /api/fs/trash`.
- `GET|POST /api/artifacts?session=` resolve / record (what `gerdr artifacts` and the UI use); `/api/folders/*` for ids.
- Watch subscriptions on /ws.
- Same guard as the other `/api/fs/*` (whoever holds the token can already make an agent run any command). Through
  `/h/<host>/` for another computer; `gerdr folder pull/push` already syncs a folder between computers.

## Team: sharing a task's artifacts (proposed 2026-11-12, not agreed yet)

Where the team side stands (gerdr-team `DESIGN.md` / `PROTOCOL.md`, the team panel's `TEAM-V2.md`):
- teamd (v1) already stores **a shared folder per requirement**: uploads by path, a new upload of the same path is a
  version, files by sha256 on disk, trash for 30 days, `GET /api/artifacts/{id}` with Range. Nothing fetched by itself.
- v2 (tasks + team messages) says **nothing is shared by default** (sessions, chats, files stay on the computer doing
  the work), files go as attachments of a team message (`files` is passed through, uploads "come later"), and drops the
  shared folder. The one opt-in since: the assignee may **share the agent's conversation** with the task's creator
  (none / view / view + talk); the computer pushes a copy every 15 s, others read the copy, never the computer.

Proposal, same shape as the conversation share: **a copy, one-way, opt-in.**
- A team task taken on a computer is a task there, so it has an artifacts folder there (owner `{panel, task, team}`).
  That computer is the only writer; the server holds a copy; others read the copy (their computer may be offline, the
  worker's too).
- **Records always go up** with the task: links, text, pinned files' names. They are tiny and they are the answer to
  "where is the PR". A `path` record shows as "on B's computer: ~/dev/xxx" (not reachable, but it says where).
- **Files go up when shared**: the same switch as the conversation (not shared / the creator can see), or by attaching
  them to a team message / to "reply and deliver" (the explicit hand-over, which preselects pinned files). Shared:
  every change is uploaded (by sha256, so unchanged files cost nothing; `.gerdrignore` applies, so `草稿/` and caches
  stay home; a size cap per file, a setting). Deleting locally moves it to the server's trash.
- Others see it read-only in the team task's drawer with the same `<Folder>` (a remote source: list + raw URL from the
  server, no writes), and can "take a copy" into their own task's folder; their agents get the same through a tool /
  `gerdr artifacts` on a team source. Not two-way: a teammate adds files by attaching them to a team message, so there is
  never a conflict to settle.
- Server: reuse v1's folder storage, keyed by v2 task instead of requirement (so v2's "shared folder dropped" becomes
  "kept as the copy of a task's artifacts"); a change bumps the task's rev like messages do.
- So `<Folder>` takes a source from the start: local (`/api/fs/*` on this or another of my computers) or team
  (read-only, the server's copy).

## Conventions stay in prose

Gerdr knows nothing about `稿子.md` or platforms. The self-media layout (brief, copy files with frontmatter, a directory
per platform / version, scratch in `草稿/`) is a skill (the panel's `skills/`, or the user's own), as in Annulo, where it
is in `tasks/work-on-content.md`, not in code.

## As built (2026-11-12)

- `internal/artifacts`: the root (config.json `artifactsRoot`, default `~/Gerdr`), `ForRepo` (git common dir → folder,
  `name_2` when another repository of the same name has it), `Ensure` (identity, `.gerdrignore`, index), records
  (`Record` / `Items`), `List`, `Trash`. A directory an agent made by itself is adopted the first time it is looked at.
- Sessions: `hub.Record.ArtifactsDir` (explicit, kept across agent switches; `server.SessionArtifacts` gives only that:
  Code sessions have none). Handed to the adapter as `_meta gerdr.artifactsDir`:
  `agentwire.SessionPrompt` adds the "Artifacts folder" section; claude gets `--add-dir` once the folder exists (it
  refuses a missing one, so the very first write may ask); codex gets it as a `writableRoots` of workspace-write.
- API: `server/artifactsapi.go`; `/api/fs/stat?hash=1` gives the sha256 an editor saves against (`PUT /api/fs/file?ifMatch=`).
- CLI `gerdr artifacts` (`cmd/gerdr/artifacts.go`); auto-approved for agents (`sysenv.SafeGerdrCLI`) unless it names
  `--dir` / `--session` (Gerdr would write a `.gerdr/` wherever told). The built-in gerdr skill documents it.
- UI: `components/folder/` (`Folder`, `Records`, `MdEditor`); the folder button in a session pane's title bar (only a session with an `artifactsDir`) opens it
  as a pane beside (a `file` pane with `folder: true`, `actions/files.ts` `openArtifacts`). ⌘S saves inside the editor:
  elements can claim keys from the global shortcuts with `data-own-keys` (`lib/commands.ts`). Editor styles:
  `src/folder.css`. tiptap is loaded only when a .md is opened, and imported only through `@tiptap/react` + extension
  packages (never `@tiptap/core` directly: a second copy from another esm.sh URL breaks it).
- Panels: `ctx.artifacts` (`folder({ key, name, under })` → path by owner `{panel, task}`, `rename`, `items`,
  `record`; `work/host.go`, `server/workhost.go`), `ctx.sessions.start({ artifactsDir })`, `ArtifactsFolder` from
  `@gerdr/core`. A folder recorded into before its owner looked (no owner yet) is claimed by it, not given a `name_2`. Folder names have no spaces (runs of whitespace and characters unsafe in a path become one `_`): a path with spaces has to be quoted in every command.
- Checked in a debug instance with playwright: records, tree, preview, rich editing (a save changes only the edited
  line: front matter, compact tables, `snake_case` kept), the "changed meanwhile" bar.

## Order

1. Core: root + `folder.json` + records, `list/move/trash` + watch, `<Folder>` with records and Markdown editing,
   session `artifactsDir` + prompt section, `gerdr artifacts`, the skill text.
2. Code: the Files pane, `artifactsDir` per repository.
3. Superagent: a folder per task, the Files tab, `links` → records, attachments → `素材/`, a self-media skill.
4. Team: records up with the task, the share switch for files, the read-only view, attachments on messages / delivery
   (server: v1 folder storage keyed by v2 task).
5. Later: picking files to publish (Annulo's `FolderPublish`), auto-recording PR URLs seen in tool output and folders
   created outside the repo (only if agents forget to record them often enough to matter).
