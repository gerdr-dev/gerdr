// What a local function gets as its second argument: `export async function name(input, ctx: Ctx)`.
// This file is the reference (gerdr installs and updates it; don't edit it). It declares globals, so no import is needed.
// Every async call finishes before the next line runs (there's no event loop): `await` works as usual.

interface Ctx {
  /**
   * Runs a command-line tool. A non-zero exit isn't an error: look at `code`. Only a missing command, a timeout or a bad
   * `cwd` reject. The command and its arguments go apart, with no shell in between: put URLs and user input straight
   * into the array, never into a command string. For pipes: `ctx.exec('sh', ['-c', 'git log "$1" | head', '_', branch])`.
   * Commands are found on the user's login shell PATH. stdin is empty unless `input` is given.
   */
  exec(cmd: string, args?: string[], opts?: ExecOptions): Promise<ExecResult>;
  exec(cmd: string, opts: ExecOptions): Promise<ExecResult>;

  /** Fetches 4 at a time, responses in order; a failed one is `{ ok: false, status: 0, url, error }` and doesn't fail the rest. */
  fetchAll(requests: (string | { url: string; method?: string; headers?: Record<string, string>; body?: string | object })[]): Promise<FetchResponse[]>;

  /** Parses HTML. */
  html(text: string): {
    find(selector: string): { tag: string; text: string; attrs: Record<string, string> }[];
    /** The visible text. */
    text(): string;
    markdown(): string;
  };

  /**
   * Calls an MCP tool (the `mcp__<server>__<tool>` an agent sees): structuredContent, else the text parsed as JSON, else
   * the text; a tool error rejects. Servers: gerdr's own (Settings → MCP), then Claude Code's, then Codex's. A platform's
   * data goes through its MCP when there is one; wrap it in one file (`local/_okr.ts`).
   */
  mcp: {
    (server: string, tool: string, args?: Record<string, unknown>): Promise<any>;
    /** What's configured: to show a feature only when its server is there, rather than guessing from errors. */
    servers(): { name: string; status: "enabled" | "disabled"; source: "gerdr" | "claude" | "codex" }[];
  };

  secrets: {
    /**
     * A key the user stored with `gerdr work secret set NAME`; null when there's none (throw an error saying which
     * command to run). Pass it to commands through `exec`'s `env`, never as an argument; never log it.
     */
    get(name: string): string | null;
  };

  /**
   * Files in this panel's directory: data a function keeps between runs, exports, reports. Paths are relative with `/`;
   * no absolute paths, no `..`. All synchronous. Keep data in `user/data/`: `user/` is this person's and never shared (AGENTS.md).
   * For anything outside it (other repositories, the user's folders) use `ctx.dirs`.
   */
  fs: {
    /** This panel directory's absolute path. */
    root: string;
    /** All levels down, skipping `.` entries and node_modules, 5000 at most; a missing dir is []. */
    list(dir?: string): FileInfo[];
    stat(path: string): FileInfo | null;
    /** The text, 10 MB at most. */
    read(path: string): string;
    /** Creates directories. With the etag you read, a file changed since isn't overwritten: it throws. */
    write(path: string, text: string, opts?: { etag?: string }): { etag: string };
    mkdir(path: string): void;
    /** Throws if `to` exists. */
    move(from: string, to: string): void;
    /** Throws if `to` exists. */
    copy(from: string, to: string): void;
    /** Into the system Trash. */
    trash(path: string): void;
    /** Opens it with its app; reveal: shows it in Finder instead. */
    open(path: string, opts?: { reveal?: boolean }): void;
  };

  /**
   * Conversations with coding agents (the Code side of gerdr): hand a task to an agent and follow it.
   * A conversation started here shows up in this project's Code layout as a new tab — in the workspace for its directory
   * (or the closest one above it), else a new workspace for it (or nowhere: `workspace: "none"`) — and keeps running when
   * the page closes.
   */
  sessions: {
    /**
     * Opens a conversation in `cwd` and sends `prompt` as its first message. Resolves once it's sent (not when the agent
     * is done): follow it with `get`. To point the agent at other repositories, name them in the prompt.
     */
    start(opts: {
      /** Absolute or `~/…`. Must exist: check with `ctx.dirs.stat`. */
      cwd: string;
      prompt?: string;
      /** `claude`, `codex`, `native`, or a variant's id from settings. Default: the user's default agent. */
      agent?: string;
      /** The tab's name until the agent names the conversation itself. */
      title?: string;
      /** Permission mode (claude: default / acceptEdits / plan / auto; codex: ask / auto / read-only). Default: the user's default, unless that one approves everything. */
      mode?: string;
      /**
       * The model, as the agent's model picker lists it (claude: `sonnet` / `haiku` / …; codex and native: their own
       * ids). Default: the user's default for that agent. One the agent doesn't offer is an error that lists the ones it does.
       */
      model?: string;
      /** Thinking effort (`low` / `medium` / `high` / …, whatever the agent and model offer). Default: the user's default. */
      effort?: string;
      /**
       * The session handing it the work (its `parent`). Default: the session that called this function, if any (an agent
       * running `gerdr work run …` or calling it as a tool), so chains of who-started-whom stay unbroken.
       */
      parent?: string;
      /**
       * Which Code workspace it goes in: a workspace id from `ctx.workspaces.list()`, or `"new"` for a new one named
       * after the directory. Default: the workspace whose directory is `cwd` (or the closest one above it), else a new one.
       * `"none"`: no tab in Code at all, for a conversation the page shows itself (`SessionChat` from @gerdr/core);
       * going to it in Code (openInCode) opens a tab then.
       */
      workspace?: string;
      /** Switch the open windows to it. Default false: don't pull the user away from what they're doing. */
      focus?: boolean;
      /** Labels to find it by later (`list({ tags })`), e.g. `{ task: "t12", mr: "group/repo/34" }`. Gerdr gives them no meaning. */
      tags?: Record<string, string>;
      /**
       * A session that gets a message each time this one stops (finished its turn, waiting for a person, failed), with
       * its reply: `"work"` = this panel's agent conversation, else a session id. Reports wait while the target
       * is busy and go as one message, so the coordinator isn't blocked on the worker nor has to poll it.
       */
      reportTo?: string;
      /**
       * Files on this machine sent with the first message, as Gerdr's own input box sends them: an image as the image,
       * anything else as a file link the agent opens with its tools. A page gets a path for a file the user pasted,
       * dropped or picked by POSTing it to `/api/fs/upload?name=<file name>` (body = the file; returns { path }).
       */
      attachments?: string[];
    }): Promise<Session>;
    /**
     * The agents and roles `start` takes as `agent`. A role (one the user set up in Settings → Agent) has a `base`
     * (claude / codex / native) and usually a `prompt`: what it's for, in the user's words — what to pick it by.
     */
    agents(): AgentInfo[];
    /** With `reply`: what the agent said since the last message sent to it. null when there's no such conversation. */
    get(id: string): (Session & { reply: string }) | null;
    /**
     * Every conversation, most recently active first; with `cwd`, only the ones in that directory; with `tags`, only
     * the ones carrying all of them (`""` as the value: any value).
     */
    list(opts?: { cwd?: string; tags?: Record<string, string> }): Session[];
    /**
     * Sends another message. A running conversation gets it at its next pause (between steps); a closed one is opened again.
     * `"work"` = this panel's agent conversation (e.g. to hand it something to decide).
     * `now`: right away, like the input box's "send now" — a running command moves to the background and the agent
     * reads the message at once, so a worker busy with a long step still takes it.
     */
    send(id: string, text: string, opts?: { attachments?: string[]; now?: boolean }): Promise<Session>;
    /**
     * Stops its agent and takes it off the screen, like closing its tab: the conversation is kept and `send` carries on
     * from where it was. For a worker whose task is done or parked, so it stops holding a process.
     */
    close(id: string): Promise<void>;
    /** Changes its tags: a value sets that key, `""` removes it, keys not named stay. */
    tag(id: string, tags: Record<string, string>): Session;
  };

  /** The Code side's workspaces (not this panel: that's `ctx.workspace`). */
  workspaces: {
    /** This project's workspaces in their order; `all`: every project's. */
    list(opts?: { all?: boolean }): Workspace[];
  };

  /**
   * Directories anywhere on disk, read-only: for picking where a task runs and learning what a repository is.
   * Paths are absolute or `~/…`.
   */
  dirs: {
    /** The user's home directory. */
    home(): string;
    /** Directories conversations ran in and workspaces point at, most recently used first: the user's projects. */
    recent(): { path: string; /** ms; 0 for a workspace's directory no conversation used */ lastUsed: number; source: "session" | "workspace" }[];
    /** The folders in a directory (dot folders skipped), by name. Throws when it doesn't exist. */
    list(path: string): { name: string; path: string; /** a git repository */ git: boolean }[];
    /** null when nothing's there. */
    stat(path: string): { path: string; dir: boolean; git: boolean } | null;
    /** A text file (a README, a CLAUDE.md), cut at `max` bytes: 64 KB by default, 1 MB at most. null when missing. */
    read(path: string, opts?: { max?: number }): string | null;
  };

  /**
   * Events (FUNCTIONS.md "On an event"): things that happened outside a conversation, one record for every source.
   * A poll on a schedule emits what it found; triggers/*.json (here or in any panel), watching sessions and add-ons
   * get it. Give a stable `id` and finding the same thing on the next poll emits nothing (`fresh: false`).
   */
  events: {
    emit(e: {
      /** Stable id of this happening ("gitlab:note:123"); one already seen is dropped. Leave out to always emit. */
      id?: string;
      /** Who it comes from: gitlab, ci, slack… [A-Za-z0-9._-], no "/". */
      source: string;
      /** What happened, dotted: mr.note, pipeline.failed, message. */
      kind: string;
      /** What it is about (group/repo!34, a channel id): what subscribers narrow by. */
      key?: string;
      /** Who did it, in the source's own identities (a GitLab username); leave out when nobody did (a pipeline). */
      actor?: string;
      title?: string;
      text?: string;
      url?: string;
      data?: unknown;
    }): { event: GerdrEvent; fresh: boolean };
    /** Oldest first. since: unix ms (default: the last 24 hours); on: "source/kind" with * globs; limit: the newest n (100). */
    list(q?: { since?: number; on?: string; key?: string; limit?: number }): GerdrEvent[];
    /**
     * What can be subscribed to, for a picker ("which events make a task?"): what the switched-on add-ons say they
     * emit, then source/kinds seen in the last 30 days. Never list sources by name in code: whoever has no Slack
     * add-on sees no Slack.
     */
    catalog(): { on: string; title?: string; description?: string; addon?: string; canReply?: boolean; /** ms, latest seen */ seen?: number }[];
    /**
     * Answers an event through its own reply address (`event.reply`, set by the source: a chat message's thread),
     * the same way for every source. `data` adds fields the source understands. Throws when the event has none.
     */
    reply(event: GerdrEvent, text: string, data?: Record<string, unknown>): { event: GerdrEvent; fresh: boolean };
  };

  /** Progress the page shows while the function runs (and `gerdr work run` prints). */
  progress(p: { done?: number; total?: number; message?: string }): void;
  /** A log line, shown live and kept in the run log (like console.log). Never log keys, cookies or tokens. */
  log(...args: unknown[]): void;
  /** Waits; 60 s at most per call. */
  sleep(ms: number): Promise<void>;
  /** Who called this function: as a panel tool (`export const tools`), the conversation's id; otherwise "". */
  caller: { session: string };
  /** This panel: its id, this directory, where its run logs are. */
  workspace: { project: string; dir: string; logs_dir: string };
  /** gerdr's interface language, for messages meant for the user. */
  locale: "zh";
}

interface ExecOptions {
  /** Relative to this panel's directory and can't leave it: for another repository pass its path as an argument (`git -C ~/dev/x`). */
  cwd?: string;
  /** stdin. */
  input?: string;
  /** ms; 30 s by default, 10 min at most. The whole process tree is killed on timeout or stop. */
  timeout?: number;
  /** Added to gerdr's environment: pass keys here (`{ TOKEN: ctx.secrets.get('TOKEN') }`). */
  env?: Record<string, string>;
}

interface ExecResult {
  code: number;
  stdout: string;
  stderr: string;
  /** stdout / stderr keep 10 MB each. */
  truncated: boolean;
}

interface FetchResponse {
  ok: boolean;
  status: number;
  statusText: string;
  url: string;
  headers: { get(name: string): string | null };
  text(): Promise<string>;
  json(): Promise<any>;
  arrayBuffer(): Promise<ArrayBuffer>;
  timing: { ttfb_ms: number; total_ms: number };
  /** The body was over 10 MB and got cut. */
  truncated: boolean;
  error?: string;
}

interface FileInfo {
  path: string;
  dir: boolean;
  size: number;
  /** ms */
  mtime: number;
  etag: string;
}

interface GerdrEvent {
  id: string;
  source: string;
  kind: string;
  key?: string;
  /** Who did it, in the source's own identities (a Slack member id); absent when nobody did (a pipeline). */
  actor?: string;
  title?: string;
  text?: string;
  url?: string;
  data?: any;
  /** Where an answer goes (ctx.events.reply uses it); absent when there's nowhere to answer. */
  reply?: { kind: string; data?: any };
  /** unix ms */
  at: number;
  /** How many triggers deep it was emitted; past 4 it isn't handed on (two triggers feeding each other stop). */
  depth?: number;
}

interface AgentInfo {
  id: string;
  name: string;
  /** A role's engine: claude / codex / native. Absent for those three themselves. */
  base?: string;
  /** Found on this machine (a CLI that isn't installed can't start). */
  available: boolean;
  /** A role's own instructions: what it is for. */
  prompt?: string;
  error?: string;
}

interface Session {
  id: string;
  title: string;
  /** Which agent: claude / codex / native / a variant's id. */
  agent: string;
  cwd: string;
  /** waiting: it asks the user something (an approval, a question) — they answer in gerdr's window; `waiting` says what. */
  status: "starting" | "running" | "waiting" | "idle" | "error";
  waiting?: string;
  /** ms: since when it's been waiting for a person (only while waiting). */
  waitingSince?: number;
  /** ms: when the running turn started (only while running). */
  turnStartedAt?: number;
  /**
   * The last turn came from the user typing in this conversation (not from code or another agent): they've read
   * its reply. E.g. a board shouldn't ask them to "come and look" at an answer to their own question.
   */
  byPerson?: boolean;
  error?: string;
  /** See `sessions.start`; `{}` when it has none. */
  tags: Record<string, string>;
  /** The session it reports to, if any. */
  reportTo?: string;
  /**
   * The session that started it (an agent's `gerdr session new`, or a function that agent ran). Follow `parent` up and
   * look for sessions whose parent is this one to draw who handed what to whom, any number of levels deep.
   */
  parent?: string;
  /** ms */
  createdAt: number;
  /** ms: the last thing that happened in it (a message, agent output, an approval). */
  updatedAt: number;
}

interface Workspace {
  id: string;
  name: string;
  /** Where its new tabs open; "" when it has none. */
  cwd: string;
  /** The project (gerdr session) it belongs to. */
  project: string;
  /** The conversations in its panes. */
  sessions: Session[];
}
