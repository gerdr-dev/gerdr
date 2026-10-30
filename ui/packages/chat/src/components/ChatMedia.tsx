import { AlertCircle, ExternalLink, FileText, FolderOpen, Loader2, RotateCw } from "lucide-react";
import { useContext, useEffect, useState } from "react";
import { chatHost } from "../host";
import { t } from "../i18n";
import { cn } from "../utils";
import { openLightbox } from "./Lightbox";
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

// Load failures by base URL, kept across remounts. A failed file shows one fixed-height chip ("loading" while retries
// run, "can't open" after), and retries probe in the background (does the file exist yet?) without putting the media
// back on screen until a probe passes. Remounting the broken <img> for each retry made the row change height at every
// step (broken image, reloading, failed chip), and the virtual list restarted the cycle whenever the row scrolled back
// into view, so a single broken image kept the chat jumping (tools/scrolltest/media.mjs).
// tries: probes used so far. show: a probe passed, render the media again (a load clears the entry, an error sends it
// back to the chip with tries kept, so a file that exists but won't decode can't loop). now: probe after a short
// pause instead of the backoff (the retry button).
type Fail = { tries: number; show: boolean; now?: boolean };
const mediaFail = new Map<string, Fail>();

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

const rawURL = (path: string, cwd?: string) => chatHost().fileURL(path, cwd);
// No fileStat on the host: assume the file is there (a retry just tries loading it again).
const statOf = (path: string, cwd?: string) => chatHost().fileStat?.(path, cwd) ?? Promise.resolve({ size: 1, mtime: 0 });
const nameOf = (p: string) => bare(p).split("/").pop() || p;

/** 一张图 / 一段视频或音频 / 一份 PDF */
export function ChatMedia({ src, alt }: { src: string; alt?: string }) {
  const session = useContext(SessionContext);
  const cwd = session?.cwd;
  const path = localPath(src);
  const k = bare(path ?? src);
  const isVideo = VIDEO.test(k);
  const isAudio = AUDIO.test(k);
  // Agents often post the reply while the file is still being written (a render finishing, an encoder rewriting the
  // mp4), so the first load can hit a missing or half-written file. Retry a few times with backoff, each with a fresh
  // query param so the browser doesn't hand back the cached failure; after that, offer a manual retry.
  const base = path ? rawURL(path, cwd) : src;
  const [attempt, setAttempt] = useState(0);
  const [fail, setFailState] = useState(() => mediaFail.get(base));
  const setFail = (f: Fail | undefined) => {
    if (f) mediaFail.set(base, f);
    else mediaFail.delete(base);
    setFailState(f);
  };
  const onError = () => {
    const f = mediaFail.get(base);
    if (!f?.show && f) return; // already showing the chip; a second report must not restart the backoff
    setFail({ tries: f?.tries ?? 0, show: false });
  };
  const onLoad = () => mediaFail.has(base) && setFail(undefined);
  const done = !!fail && !fail.show && fail.tries >= RETRY_DELAYS.length;
  useEffect(() => {
    if (!fail || fail.show || done) return;
    let live = true;
    const t = setTimeout(
      () => {
        const probe = path
          ? statOf(path, cwd).then((st) => st.size > 0)
          : VIDEO.test(k) || AUDIO.test(k)
            ? Promise.resolve(true) // remote stream: nothing cheaper to ask than trying it again
            : new Promise<boolean>((ok) => {
                const im = new Image();
                im.onload = () => ok(true);
                im.onerror = () => ok(false);
                im.src = `${src}${src.includes("?") ? "&" : "?"}r=${Date.now()}`;
              });
        probe.then(
          (ok) => {
            if (!live) return;
            setFail({ tries: fail.tries + 1, show: ok });
            if (ok) setAttempt((a) => a + 1);
          },
          () => live && setFail({ tries: fail.tries + 1, show: false }),
        );
      },
      // The retry button still waits a moment: a probe answered in a few ms swapped the icon to a spinner and back
      // within a frame or two, which read as a flicker rather than "retrying".
      fail.now ? 400 : RETRY_DELAYS[fail.tries],
    );
    return () => {
      live = false;
      clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-armed by fail only
  }, [fail]);
  // Chromium shares buffered media data between players with the same URL for the life of the page, ignoring
  // Cache-Control. Agents keep re-rendering to the same file name, so a later message's player was fed the previous
  // render's bytes (old duration) spliced with the new file's: a grey box that never plays and never errors. Putting
  // the file's mtime and size in the URL gives each version its own cache entry. Images don't go through that cache.
  const streamed = path != null && (isVideo || isAudio);
  const [version, setVersion] = useState<string | null>(null);
  useEffect(() => {
    if (!streamed) return;
    let live = true;
    setVersion(null);
    statOf(path!, cwd).then(
      (st) => live && (st.size > 0 ? setVersion(`${st.mtime}-${st.size}`) : onError()),
      () => live && onError(),
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- onError only changes with attempt
  }, [streamed, path, cwd, attempt]);
  const url = (streamed && version ? `${base}&v=${version}` : base) + (attempt && path ? `&r=${attempt}` : "");
  const name = alt || nameOf(path ?? src);
  if (fail && !fail.show) {
    // Same box while retrying and after giving up: only the icon and the words change, never the height.
    return (
      <button
        type="button"
        disabled={!done}
        title={done ? `${path ?? src}\n${t("media.clickRetry")}` : (path ?? src)}
        onClick={() => {
          // One more probe, right away. The chip stays put (spinner while it runs) and the media only comes back if the
          // file is there: putting the broken <img> back to find out made the row change height twice per click.
          setFail({ tries: RETRY_DELAYS.length - 1, show: false, now: true });
        }}
        className="my-1 inline-flex max-w-full items-center gap-1.5 rounded-md border border-border/60 px-2 py-1 align-middle text-xs text-muted-foreground enabled:hover:bg-accent"
      >
        {done ? <AlertCircle className="size-3.5 shrink-0 text-bad" /> : <Loader2 className="size-3.5 shrink-0 animate-spin" />}
        <span className="min-w-0 truncate">{done ? t("media.cantOpen", { src: path ?? src }) : t("media.loading", { src: path ?? src })}</span>
        {done && <RotateCw className="size-3.5 shrink-0" />}
      </button>
    );
  }
  let body;
  if (isVideo) {
    body = <ChatVideo key={attempt} url={streamed && !version ? undefined : url} ratioKey={base} onError={onError} onLoad={onLoad} />;
  } else if (isAudio) {
    body = (
      <audio
        key={attempt}
        src={streamed && !version ? undefined : url}
        controls
        preload="metadata"
        onError={onError}
        onLoadedMetadata={onLoad}
        className="w-80 max-w-full"
      />
    );
  } else if (PDF.test(k)) {
    body = (
      <button
        type="button"
        onClick={() => (path && chatHost().openPath ? chatHost().openPath!(path, cwd) : window.open(path ? rawURL(path, cwd) : src, "_blank", "noopener"))}
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
        title={t("media.zoom")}
        onError={onError}
        onLoad={onLoad}
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

function ChatVideo({ url, ratioKey, onError, onLoad }: { url?: string; ratioKey: string; onError: () => void; onLoad: () => void }) {
  const [ratio, setRatio] = useState(() => videoRatio.get(ratioKey) ?? 16 / 9);
  return (
    <video
      src={url}
      controls
      preload="metadata"
      onError={onError}
      onLoadedMetadata={(e) => {
        onLoad();
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
  const openNative = chatHost().openNative;
  return (
    <span className="inline-flex max-w-full items-center gap-1 text-xs text-muted-foreground">
      <span className="min-w-0 truncate" title={path}>
        {name}
      </span>
      {openNative && (
        <>
          <button type="button" className={btn} title={t("media.openWithApp")} onClick={() => openNative(path, cwd, false)}>
            <ExternalLink className="size-3.5" /> {t("media.open")}
          </button>
          <button type="button" className={btn} title={path} onClick={() => openNative(path, cwd, true)}>
            <FolderOpen className="size-3.5" /> {navigator.platform.includes("Mac") ? t("media.revealFinder") : t("media.revealFolder")}
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
