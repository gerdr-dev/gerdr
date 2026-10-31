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
   * Files in this Work directory: data a function keeps between runs, exports, reports. Paths are relative with `/`;
   * no absolute paths, no `..`. All synchronous. Keep data under a folder of its own (`data/`).
   * For anything outside it (other repositories, the user's folders) use `ctx.dirs`.
   */
  fs: {
    /** This Work directory's absolute path. */
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
   * (or the closest one above it), else a new workspace for it — and keeps running when the page closes.
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
      /** A workspace id from `ctx.workspaces.list()` to put it in, instead of finding one by `cwd`. */
      workspace?: string;
      /** Switch the open windows to it. Default false: don't pull the user away from what they're doing. */
      focus?: boolean;
    }): Promise<Session>;
    /** With `reply`: what the agent said since the last message sent to it. null when there's no such conversation. */
    get(id: string): (Session & { reply: string }) | null;
    /** Every conversation, most recently active first; with `cwd`, only the ones in that directory. */
    list(opts?: { cwd?: string }): Session[];
    /** Sends another message. A running conversation gets it when its current turn ends. */
    send(id: string, text: string): Promise<Session>;
  };

  /** The Code side's workspaces (not this Work project: that's `ctx.workspace`). */
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

  /** Progress the page shows while the function runs (and `gerdr work run` prints). */
  progress(p: { done?: number; total?: number; message?: string }): void;
  /** A log line, shown live and kept in the run log (like console.log). Never log keys, cookies or tokens. */
  log(...args: unknown[]): void;
  /** Waits; 60 s at most per call. */
  sleep(ms: number): Promise<void>;
  /** This Work project: its id, this directory, where its run logs are. */
  workspace: { project: string; dir: string; logs_dir: string };
  /** gerdr's interface language, for messages meant for the user. */
  locale: "zh";
}

interface ExecOptions {
  /** Relative to this Work directory and can't leave it: for another repository pass its path as an argument (`git -C ~/dev/x`). */
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

interface Session {
  id: string;
  title: string;
  /** Which agent: claude / codex / native / a variant's id. */
  agent: string;
  cwd: string;
  /** waiting: it asks the user something (an approval, a question) — they answer in gerdr's window; `waiting` says what. */
  status: "starting" | "running" | "waiting" | "idle" | "error";
  waiting?: string;
  error?: string;
  /** ms */
  createdAt: number;
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
