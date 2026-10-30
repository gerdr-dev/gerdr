import { chatHost } from "./host";

/**
 * 认出对话里的**本地文件路径**，让它可点（点了原地打开，见 components/FileViewer.tsx）。
 *
 * 判据照搬 herdr-web 的 term/paths.ts + lib/mdpaths.ts，每一条都是那边真机上踩出来的：
 *
 *   - agent 写路径最常见的形式不是 markdown 链接，而是「生成好了 /tmp/a.png。」—— 中文标点要当终止符，
 *     不然 `a.png。相对的` 整段被当成路径，点开报「没有这个文件」，而下划线看着完全正常。汉字本身放行（`/tmp/图表.png`）。
 *   - 光秃秃的相对路径（`docs/plan.md`）**必须带扩展名**才认：`2026/08/21`、`读/写`、`and/or` 都有斜杠。
 *   - 有根的（`/…` `~/…` `./…`）至少两段或带扩展名：挡掉「and /or」里那个 `/or`。
 *   - 截断过的（带 `…`）认不出来就是认不出来，别猜一个短的出来。
 *   - 尾巴上的标点、行号（`a.go:42:7`）剥掉；`#L21` 这种锚点打不开时再剥（`notes#1.md` 是合法文件名）。
 */

const STOP = "\\s\"'`()\\[\\]{}<>|,;。，、；：！？（）【】《》「」『』“”‘’…　│┃║─━═╭╮╰╯┌┐└┘├┤┬┴┼";

// 边界字符包在匹配里（第一组）而不用后顾断言：老 Safari 不支持 lookbehind，而正则是解析期编译的 —— 不支持就整个模块白屏。
const PATH_RE = new RegExp(
  `(^|[^\\w:/~.@+-])(` +
    `file:///[^${STOP}]*` + // file:///…
    `|(?:~|\\.{1,2})?/[^${STOP}]*` + // /… ~/… ./… ../…
    `|[\\w@][\\w.@+-]*(?:/[\\w.@+-]+)+` + // docs/plan.md
    `)`,
  "g",
);

const TAIL = /[.,;:!?)\]}'">»。，、；：！？）】》]+$/;
const LINENO = /:\d+(?::\d+)?$/;
const LINE_ANCHOR = /(?:#L?\d+(?:[-,]L?\d+)?|:\d+(?::\d+)?)$/;

/** 剥掉尾巴上的行号锚点。打开失败之后才用它重试一次（先剥的话 `notes#1.md` 那种永远打不开）。 */
export const stripLineAnchor = (p: string) => p.replace(LINE_ANCHOR, "");

export type PathHit = { path: string; start: number; end: number };

export function findPaths(line: string): PathHit[] {
  const out: PathHit[] = [];
  PATH_RE.lastIndex = 0;
  for (let m = PATH_RE.exec(line); m; m = PATH_RE.exec(line)) {
    const raw = m[2];
    const start = m.index + m[1].length;
    if (raw.includes("…") || line[start + raw.length] === "…" || m[1] === "…") continue;
    let text = raw.replace(TAIL, "");
    if (text.length > 1) text = text.replace(LINENO, "");
    const end = start + text.length;
    let path = text;
    if (path.startsWith("file://")) {
      try {
        path = decodeURIComponent(path.slice("file://".length));
      } catch {
        continue;
      }
    }
    if (!path || path === "/" || path === "~") continue;
    const rooted = /^(?:file:\/\/|~?\/|\.{1,2}\/)/.test(text);
    const hasExt = path.slice(path.lastIndexOf("/") + 1).includes(".");
    const slashes = (path.match(/\//g) ?? []).length;
    if (rooted ? slashes < 2 && !hasExt : !hasExt) continue;
    out.push({ path, start, end });
  }
  return out;
}

type HNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HNode[] };

/**
 * rehype 插件：正文里像路径的那几段换成 `<a data-gerdr-path="…">`（Markdown.tsx 的 `a` 认它）。
 * 代码块里不拆（一屏下划线是噪音）、链接里不拆（嵌套 `<a>` 非法）；内联 `code` 要拆 —— agent 写路径十有八九带反引号。
 * 用 data 属性不用假 scheme 的 href：href 会过 urlTransform / harden，不认识的协议会被清掉。
 */
export function rehypePaths() {
  return (tree: HNode) => {
    walk(tree, false);
    return tree;
  };
}

function walk(node: HNode, inPre: boolean) {
  const kids = node.children;
  if (!kids) return;
  const pre = inPre || node.tagName === "pre";
  for (let i = 0; i < kids.length; i++) {
    const k = kids[i];
    if (k.type === "element") {
      if (k.tagName !== "a") walk(k, pre);
      continue;
    }
    if (k.type !== "text" || pre || !k.value) continue;
    const hits = findPaths(k.value);
    if (!hits.length) continue;
    const out: HNode[] = [];
    let at = 0;
    for (const h of hits) {
      if (h.start > at) out.push({ type: "text", value: k.value.slice(at, h.start) });
      out.push({ type: "element", tagName: "a", properties: { dataGerdrPath: h.path }, children: [{ type: "text", value: k.value.slice(h.start, h.end) }] });
      at = h.end;
    }
    if (at < k.value.length) out.push({ type: "text", value: k.value.slice(at) });
    kids.splice(i, 1, ...out);
    i += out.length - 1;
  }
}

/** markdown 链接的 href 像不像本地路径（`[图](/Users/x/a.png)`、`file:///…`、`~/x`、`./x`）。`//host` 那种协议相对的外链不算。 */
export function localHref(h: string | undefined): string | undefined {
  if (!h || h.startsWith("//")) return;
  if (!/^(file:\/\/|~\/|\.{1,2}\/|\/)/.test(h)) return;
  // micromark 把链接目标百分号编码了（中文文件名 → %E6…），原样去 stat 一定找不到
  const raw = h.startsWith("file://") ? h.slice("file://".length) : h;
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

/**
 * Bare file names in inline code (`README.md`, `Composer.tsx:42`). Agents write these all the time, but with
 * no slash they look exactly like `acpx.GUIHint` or `Node.js`, so findPaths leaves them alone; the chat only
 * links one after the server says it opens in the session directory (as is, or by its suffix search).
 * Returns the name without the line number, or undefined if it doesn't look like a file name at all.
 */
const BARE = /^([\w@][\w.@+-]*\.[A-Za-z]\w{0,9})(?::\d+(?::\d+)?)?$/;
export const bareFileName = (text: string) => BARE.exec(text.trim())?.[1];

// Answers per cwd + name. Misses expire: the agent may create the file a few messages later, and the
// virtual list re-mounts old messages anyway, so a stale "no" fixes itself on the next look.
const known = new Map<string, { ok: boolean; at: number }>();
const MISS_TTL = 30_000;
const waiting = new Map<string, Set<string>>(); // cwd → names queued for the next batch
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setTimeout> | undefined;

const key = (name: string, cwd: string) => cwd + "\n" + name;

/** Whether `name` is known to open under `cwd`; undefined = not checked yet (one is queued). */
export function fileExists(name: string, cwd: string): boolean | undefined {
  const k = known.get(key(name, cwd));
  if (k && (k.ok || Date.now() - k.at < MISS_TTL)) return k.ok;
  let q = waiting.get(cwd);
  if (!q) waiting.set(cwd, (q = new Set()));
  q.add(name);
  // A rendered conversation asks for dozens at once: gather them into one request per cwd.
  timer ??= setTimeout(flush, 50);
  return k?.ok;
}

export function onFileExists(fn: () => void) {
  listeners.add(fn);
  return () => void listeners.delete(fn);
}

async function flush() {
  timer = undefined;
  const batches = [...waiting];
  waiting.clear();
  await Promise.all(
    batches.map(async ([cwd, names]) => {
      const list = [...names];
      try {
        const check = chatHost().fileExists;
        if (!check) throw new Error("no fileExists"); // the host can't look: bare names stay plain text
        const { exists } = await check(list, cwd);
        for (const n of list) known.set(key(n, cwd), { ok: !!exists[n], at: Date.now() });
      } catch {
        // Older server or offline: plain code for now, retried once the misses expire (not on every
        // render — the listeners below re-render, which would ask again in a tight loop).
        for (const n of list) known.set(key(n, cwd), { ok: false, at: Date.now() });
      }
    }),
  );
  for (const fn of listeners) fn();
}
