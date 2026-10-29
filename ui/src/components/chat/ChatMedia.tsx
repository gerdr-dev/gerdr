import { AlertCircle, ExternalLink, FileText, FolderOpen, RotateCw } from "lucide-react";
import { useContext, useState } from "react";
import { openPath } from "@/lib/actions";
import { cn } from "@/lib/utils";
import { isNative, nativeOpenPath } from "@/lib/native";
import { openLightbox } from "../Lightbox";
import { SessionContext } from "./sessionContext";

// 对话里的图片、视频、音频、PDF（照 shuttle 的 ChatMedia）：agent 用 Markdown 图片语法引用，
// `![说明](/绝对路径.png)` 或者相对会话目录的 `![](out/a.mp4)`，界面按扩展名画成图 / 播放器 / 卡片。
// 本机文件走 /api/fs/raw（同源、带 Range —— 视频能拖进度条；响应一律 CSP sandbox，见 server/fileview.go）；
// 网上的地址直接用。点图开灯箱（Lightbox），点 PDF 打开 gerdr 自己的文件查看器（FileViewer）；在 App 里还能用系统程序打开、在访达里显示。
// agent 怎么知道要这么写：起 agent 时给它的说明里有一句（acpx.MediaHint）。

const VIDEO = /\.(mp4|m4v|mov|webm)$/i;
const AUDIO = /\.(mp3|m4a|wav|ogg|oga|opus|aac|flac)$/i;
const PDF = /\.pdf$/i;

// Backoff (ms) between automatic reloads of a media file that failed to load.
const RETRY_DELAYS = [1000, 3000, 8000];

const bare = (s: string) => s.split(/[?#]/)[0];

/** 本机文件路径（绝对、~/、file://、或者不带协议的相对路径）；网上的地址返回 null */
export function localPath(src: unknown): string | null {
  if (typeof src !== "string" || !src.trim()) return null;
  let s = src.trim();
  try {
    s = decodeURI(s); // markdown 会把空格、中文编码掉（`![](/x/旁白 稿.png)` 进来是 %E6…）
  } catch {
    // 不是合法编码就用原样
  }
  if (/^file:\/\//i.test(s)) return s.replace(/^file:\/\//i, "");
  if (/^[a-z][a-z0-9+.-]*:/i.test(s) || s.startsWith("//")) return null; // https:、data:、blob:……
  if (s.startsWith("/api/") || s.startsWith("/_")) return null; // 本服务自己的地址
  return s;
}

const rawURL = (path: string, cwd?: string) => `/api/fs/raw?path=${encodeURIComponent(path)}&cwd=${encodeURIComponent(cwd ?? "")}`;
const nameOf = (p: string) => bare(p).split("/").pop() || p;

/** 一张图 / 一段视频或音频 / 一份 PDF */
export function ChatMedia({ src, alt }: { src: string; alt?: string }) {
  const session = useContext(SessionContext);
  const cwd = session?.cwd;
  const path = localPath(src);
  // Agents often post the reply while the file is still being written (a render finishing, an encoder rewriting the
  // mp4), so the first load can hit a missing or half-written file. Retry a few times with backoff, each with a fresh
  // query param so the browser doesn't hand back the cached failure; after that, offer a manual retry.
  const [attempt, setAttempt] = useState(0);
  const [failed, setFailed] = useState(false);
  const base = path ? rawURL(path, cwd) : src;
  const url = attempt && path ? `${base}&r=${attempt}` : base;
  const name = alt || nameOf(path ?? src);
  const onError = () => {
    if (attempt < RETRY_DELAYS.length) setTimeout(() => setAttempt((a) => a + 1), RETRY_DELAYS[attempt]);
    else setFailed(true);
  };
  if (failed) {
    return (
      <button
        type="button"
        title={`${path ?? src}\n点击重试`}
        onClick={() => {
          setFailed(false);
          setAttempt((a) => a + 1);
        }}
        className="my-1 inline-flex max-w-full items-center gap-1.5 rounded-md border border-border/60 px-2 py-1 align-middle text-xs text-muted-foreground hover:bg-accent"
      >
        <AlertCircle className="size-3.5 shrink-0 text-bad" />
        <span className="min-w-0 truncate">打不开：{path ?? src}</span>
        <RotateCw className="size-3.5 shrink-0" />
      </button>
    );
  }
  const k = bare(path ?? src);
  const isVideo = VIDEO.test(k);
  let body;
  if (isVideo) {
    body = <ChatVideo key={attempt} url={url} ratioKey={base} onError={onError} />;
  } else if (AUDIO.test(k)) {
    body = <audio key={attempt} src={url} controls preload="metadata" onError={onError} className="w-80 max-w-full" />;
  } else if (PDF.test(k)) {
    body = (
      <button
        type="button"
        onClick={() => (path ? openPath(path, cwd) : window.open(src, "_blank", "noopener"))}
        className="inline-flex max-w-full items-center gap-2 rounded-md border border-border/60 px-3 py-2 text-[13px] hover:bg-accent"
      >
        <FileText className="size-4 shrink-0 text-bad" />
        <span className="min-w-0 truncate">{name}</span>
      </button>
    );
  } else {
    body = (
      <img
        key={attempt}
        src={url}
        alt={name}
        loading="lazy"
        title="点击放大"
        onError={onError}
        onClick={(e) => openLightbox([{ url, alt: name, path: path ?? undefined, cwd }], 0, e.currentTarget)}
        className="max-h-72 max-w-[min(24rem,100%)] cursor-zoom-in rounded-md border border-border/60"
      />
    );
  }
  return (
    // 独占一行（flex 不是 inline-flex）：agent 常把图和说明写在同一段（「![图](a.png)\n**a.png**：……」），
    // 行内排的话说明挤到图的右边、和图顶端对齐，很难看（截图里看到的）
    // Videos take the full column: their width is a percentage of this span, and w-fit would size the span from the
    // video's pre-metadata default width (300px), so the reserved box came out half size until the video loaded.
    <span className={cn("my-1 flex max-w-full flex-col items-start gap-0.5", isVideo ? "w-full" : "w-fit")}>
      {body}
      {path && <LocalActions path={path} cwd={cwd} name={name} />}
    </span>
  );
}

// Aspect ratio of every video seen on this page, by URL. A <video> renders at the browser default 300x150 until its
// metadata arrives, then grows to its real size; in the virtualized chat that happens on every remount (scroll the
// row out and back), so the row's height swung by ~170px each time and the list jumped. Reserving the box from a
// known ratio keeps the height fixed: remounts reuse the measured ratio, unseen videos guess 16:9 (most screen
// recordings and renders), which is exact for those and changes at most once for the rest.
const videoRatio = new Map<string, number>();

function ChatVideo({ url, ratioKey, onError }: { url: string; ratioKey: string; onError: () => void }) {
  const [ratio, setRatio] = useState(() => videoRatio.get(ratioKey) ?? 16 / 9);
  return (
    <video
      src={url}
      controls
      preload="metadata"
      onError={onError}
      onLoadedMetadata={(e) => {
        const v = e.currentTarget;
        if (!v.videoWidth || !v.videoHeight) return;
        const r = v.videoWidth / v.videoHeight;
        videoRatio.set(ratioKey, r);
        if (Math.abs(r - ratio) > 0.01) setRatio(r);
      }}
      // Same bounds as before (at most 20rem tall, never wider than the column), expressed as a width so the
      // height follows from aspect-ratio and is known before any frame loads.
      style={{ aspectRatio: ratio, width: `min(100%, ${(20 * ratio).toFixed(3)}rem)` }}
      className="h-auto rounded-md border border-border/60 bg-black/5"
    />
  );
}

/** 本机文件下面那行：文件名 + （App 里）用系统程序打开、在访达里显示 */
function LocalActions({ path, cwd, name }: { path: string; cwd?: string; name: string }) {
  const btn = "inline-flex h-6 shrink-0 items-center gap-1 rounded-md px-1.5 hover:bg-accent hover:text-foreground";
  return (
    <span className="inline-flex max-w-full items-center gap-1 text-xs text-muted-foreground">
      <span className="min-w-0 truncate" title={path}>
        {name}
      </span>
      {isNative() && (
        <>
          <button type="button" className={btn} title="用系统默认程序打开" onClick={() => nativeOpenPath(path, cwd, false)}>
            <ExternalLink className="size-3.5" /> 打开
          </button>
          <button type="button" className={btn} title={path} onClick={() => nativeOpenPath(path, cwd, true)}>
            <FolderOpen className="size-3.5" /> {navigator.platform.includes("Mac") ? "在访达中显示" : "在文件夹中显示"}
          </button>
        </>
      )}
    </span>
  );
}

// ---- 多张图排成网格 ----
// hast 的节点类型（只用到这几个字段；不为了类型多装一个 @types/hast）
type Text = { type: "text"; value: string };
type Element = { type: "element"; tagName: string; properties: Record<string, unknown>; children: ElementContent[] };
type ElementContent = Element | Text | { type: "comment"; value: string };
type RootContent = ElementContent;
type Root = { type: "root"; children: RootContent[] };

// agent 把几张图连着写（每行一张），markdown 里是同一段；中间空一行就是挨着的几段。rehypeImageGroups 把挨着的
// 「只有图」的段落合成一段，Markdown 的 p 组件再用 imageGroup 判断：两张以上、都是图（没有视频 / 音频 / PDF）就画成网格。

const isBlank = (n: ElementContent) => (n.type === "text" && !n.value.trim()) || (n.type === "element" && n.tagName === "br");
const isImageOnly = (n: ElementContent | RootContent): boolean =>
  n.type === "element" &&
  n.tagName === "p" &&
  n.children.some((c) => c.type === "element" && c.tagName === "img") &&
  n.children.every((c) => isBlank(c) || (c.type === "element" && c.tagName === "img"));

export function rehypeImageGroups() {
  const merge = (parent: Root | Element) => {
    const out: typeof parent.children = [];
    let group: Element | null = null;
    for (const c of parent.children) {
      if (isImageOnly(c)) {
        const kids = (c as Element).children.filter((x) => !isBlank(x));
        if (group) group.children.push(...kids);
        else {
          group = { ...(c as Element), children: kids };
          out.push(group);
        }
        continue;
      }
      if (group && c.type === "text" && !c.value.trim()) continue; // 段落之间的换行
      group = null;
      if (c.type === "element") merge(c);
      out.push(c as never);
    }
    parent.children = out as never;
  };
  return (tree: Root) => merge(tree);
}

/** 段落里两张以上图、没有别的（也没有视频 / 音频 / PDF）时，返回这些图 */
export function imageGroup(node: Element | undefined): { src: string; alt: string }[] | null {
  if (!node || !isImageOnly(node)) return null;
  const imgs = node.children.filter((c): c is Element => c.type === "element" && c.tagName === "img");
  const list = imgs.map((i) => ({ src: String(i.properties.src ?? ""), alt: String(i.properties.alt ?? "") }));
  if (list.length < 2 || list.some((i) => !i.src || VIDEO.test(bare(i.src)) || AUDIO.test(bare(i.src)) || PDF.test(bare(i.src)))) return null;
  return list;
}

/** 多张图：固定大小的方形缩略图，按宽度换行；点开灯箱，能左右翻 */
export function MediaGrid({ items }: { items: { src: string; alt: string }[] }) {
  const session = useContext(SessionContext);
  return (
    <div className="my-1 flex flex-wrap gap-1.5">
      {items.map((it, i) => {
        const path = localPath(it.src);
        const url = path ? rawURL(path, session?.cwd) : it.src;
        return (
          <button
            key={i}
            type="button"
            title={it.alt || nameOf(path ?? it.src)}
            onClick={(e) =>
              openLightbox(
                items.map((x) => {
                  const p = localPath(x.src);
                  return { url: p ? rawURL(p, session?.cwd) : x.src, alt: x.alt || nameOf(p ?? x.src), path: p ?? undefined, cwd: session?.cwd };
                }),
                i,
                e.currentTarget,
              )
            }
            className="size-28 cursor-zoom-in overflow-hidden rounded-md border border-border/60"
          >
            <img src={url} alt={it.alt} loading="lazy" className="size-full object-cover" />
          </button>
        );
      })}
    </div>
  );
}
