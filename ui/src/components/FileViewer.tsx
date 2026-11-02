import { Copy, Download, ExternalLink } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { MessageResponse } from "@/components/ai-elements/message";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { openPath } from "@/lib/actions";
import { isAndroid, postAndroid } from "@/lib/android";
import { api, fileRawURL, type FileStat } from "@/lib/api";
import { isNative, nativeOpenURL } from "@/lib/native";
import { set, useStore } from "@/lib/store";
import { t } from "@/lib/i18n";

/** 文本最多读这么多：再大的多半是日志 / 数据文件，整个塞进 DOM 手机上会卡死，给下载。 */
const TEXT_MAX = 2 << 20;

const fmtSize = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`);
const shortPath = (p: string) => p.replace(/^\/(?:Users|home)\/[^/]+/, "~");

/**
 * 对话里点了一条路径之后的查看器（照 herdr-web 的 FileViewer）：图片、视频、音频、PDF 原地看，文本 / Markdown 原地读，
 * 别的给下载。手机上铺满整屏 —— 弹窗那点大小看图看视频都不够。
 * 只读：改文件是 agent 的事，这里多一个编辑器就多一份「两边同时改」的麻烦。
 */
export function FileViewer() {
  const d = useStore((s) => s.dialog);
  const stat = d?.kind === "file" ? d.stat : null;
  const close = () => set({ dialog: null });
  return (
    <Dialog open={!!stat} onOpenChange={(o) => !o && close()}>
      <DialogContent className="flex max-h-[90vh] w-full max-w-4xl flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl max-md:h-full max-md:max-h-none max-md:max-w-full max-md:rounded-none max-md:border-0">
        {stat && <Body stat={stat} />}
      </DialogContent>
    </Dialog>
  );
}

/**
 * 短写的路径（`chat/Composer.tsx`）在会话目录里对上了好几个文件：列出来挑一个。
 * 前缀淡、对上的那截正常色 —— 几条长得差不多，要一眼看出差在哪段目录。
 */
export function FilePicker() {
  const d = useStore((s) => s.dialog);
  const pick = d?.kind === "pickFile" ? d : null;
  // the path sits mid-sentence, in its own <code>: split the message around it
  const matches = t("fileViewer.matches", { n: pick?.candidates.length ?? 0, path: "\u0000" }).split("\u0000");
  return (
    <Dialog open={!!pick} onOpenChange={(o) => !o && set({ dialog: null })}>
      <DialogContent className="max-w-lg gap-3">
        <DialogTitle className="text-[14px]">{t("fileViewer.whichOne")}</DialogTitle>
        <DialogDescription className="text-[12px]">
          {matches[0]}
          <code className="font-mono">{pick?.path}</code>
          {matches[1]}
        </DialogDescription>
        <div className="-mx-2 flex max-h-[60vh] flex-col overflow-auto">
          {pick?.candidates.map((c) => {
            const s = shortPath(c);
            const head = s.slice(0, s.length - pick.path.length);
            return (
              <button
                key={c}
                className="truncate rounded-md px-2 py-1.5 text-left font-mono text-[12px] hover:bg-muted"
                title={c}
                onClick={() => openPath(c, undefined, { beside: pick.beside })}
              >
                <span className="text-muted-foreground">{head}</span>
                {s.slice(head.length)}
              </button>
            );
          })}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function Body({ stat }: { stat: FileStat }) {
  return (
    <>
      <div className="flex shrink-0 items-center gap-2 border-b py-2 pr-12 pl-4">
        <div className="min-w-0 flex-1">
          <DialogTitle className="truncate text-[14px]">{stat.name}</DialogTitle>
          <DialogDescription className="truncate font-mono text-[11px]" title={stat.path}>
            {fmtSize(stat.size)} · {shortPath(stat.path)}
          </DialogDescription>
        </div>
        <FileActions stat={stat} />
      </div>
      <div className="min-h-0 flex-1 overflow-auto overscroll-contain">
        <FileContent stat={stat} />
      </div>
    </>
  );
}

/** Copy path / open outside / download: the viewer dialog's header, and a file pane's title bar (Panes.tsx). */
export function FileActions({ stat, small }: { stat: FileStat; small?: boolean }) {
  const btn = small ? "size-6" : "size-8";
  const ic = small ? "size-3.5" : "size-4";
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className={btn}
        title={t("fileViewer.copyPath")}
        onClick={() => {
          navigator.clipboard.writeText(stat.path);
          toast(t("fileViewer.pathCopied"));
        }}
      >
        <Copy className={ic} />
      </Button>
      {isNative() || isAndroid() ? (
        <Button variant="ghost" size="icon" className={btn} title={t("fileViewer.openInBrowser")} onClick={() => openOutside(stat)}>
          <ExternalLink className={ic} />
        </Button>
      ) : (
        <Button variant="ghost" size="icon" className={btn} title={t("fileViewer.openInNewTab")} asChild>
          <a href={fileURL(stat)} target="_blank" rel="noreferrer">
            <ExternalLink className={ic} />
          </a>
        </Button>
      )}
      <Button variant="ghost" size="icon" className={btn} title={t("fileViewer.download")} asChild>
        <a href={fileRawURL(stat.path, true)} download={stat.name}>
          <Download className={ic} />
        </a>
      </Button>
    </>
  );
}

// Versioned so a file rewritten in place isn't played from Chromium's per-URL media buffer (see ChatMedia).
const fileURL = (stat: FileStat) => `${fileRawURL(stat.path)}&v=${stat.mtime}-${stat.size}`;

/** The file itself, drawn by kind: the viewer dialog's body, and a file pane's (Panes.tsx; fill: as tall as the pane). */
export function FileContent({ stat, fill }: { stat: FileStat; fill?: boolean }) {
  const url = fileURL(stat);
  const kind = stat.mime.split("/")[0];
  return kind === "image" ? (
    <div className="flex min-h-full items-center justify-center bg-muted/40 p-3">
      <img src={url} alt={stat.name} draggable={false} className="max-h-full max-w-full object-contain" />
    </div>
  ) : kind === "video" ? (
    // playsInline：iOS 上不加会直接进系统全屏播放器，关掉之后回不到这儿
    <div className="flex min-h-full items-center justify-center bg-muted/40">
      <video src={url} controls playsInline className={fill ? "max-h-full max-w-full" : "max-h-[80vh] max-w-full max-md:max-h-full"} />
    </div>
  ) : kind === "audio" ? (
    <div className="flex justify-center p-8">
      <audio src={url} controls className="w-full max-w-md" />
    </div>
  ) : stat.mime === "application/pdf" ? (
    canShowPDF() ? (
      <iframe src={url} title={stat.name} className={fill ? "h-full w-full" : "h-[80vh] w-full max-md:h-full"} />
    ) : (
      <NoPreview stat={stat} why={t("fileViewer.noPdf")} />
    )
  ) : (
    <TextBody stat={stat} url={url} />
  );
}

/** 文本（含认不出类型的：先读进来看，有 NUL 字节就是二进制）。 */
function TextBody({ stat, url }: { stat: FileStat; url: string }) {
  const [text, setText] = useState<string | null>(null);
  const [why, setWhy] = useState("");
  useEffect(() => {
    if (stat.size > TEXT_MAX) {
      setWhy(t("fileViewer.tooBig", { size: fmtSize(stat.size) }));
      return;
    }
    let live = true;
    fetch(url, { credentials: "same-origin" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(r.statusText))))
      .then((body) => {
        if (!live) return;
        if (body.includes("\0")) setWhy(t("fileViewer.binary"));
        else setText(body);
      })
      .catch((e) => live && setWhy(t("fileViewer.cantRead", { error: e.message })));
    return () => {
      live = false;
    };
  }, [url, stat.size]);
  if (why) return <NoPreview stat={stat} why={why} />;
  if (text === null) return <div className="p-10 text-center text-[13px] text-muted-foreground">{t("fileViewer.reading")}</div>;
  if (stat.mime === "text/markdown") {
    const { meta, body } = splitFrontMatter(text);
    return (
      <div className="gerdr-md mx-auto max-w-[860px] px-5 py-4">
        {meta !== null && <FrontMatter raw={meta} />}
        <MessageResponse>{body}</MessageResponse>
      </div>
    );
  }
  return <pre className="min-w-max p-4 font-mono text-[12px] leading-relaxed whitespace-pre">{text}</pre>;
}

/**
 * YAML front matter (`---` … `---` at the very top: SKILL.md, memory files, docs sites). Markdown has no such thing,
 * so left in the body it rendered as a rule followed by one run-on paragraph of `key: value` text.
 */
function splitFrontMatter(text: string): { meta: string | null; body: string } {
  const m = /^\uFEFF?---[ \t]*\r?\n([\s\S]*?)\r?\n---[ \t]*(?:\r?\n|$)/.exec(text);
  return m ? { meta: m[1], body: text.slice(m[0].length) } : { meta: null, body: text };
}

/**
 * Front matter as `key: value` pairs. Only the flat shape these files use: scalars, quoted strings, `|` / `>` blocks
 * and indented continuation lines (lists, nested maps kept as written). Anything else is shown as the raw YAML
 * rather than guessed at — no YAML library for a header.
 */
function parseFrontMatter(raw: string): [string, string][] | null {
  const out: [string, string, string[]][] = []; // key, inline value, continuation lines
  for (const line of raw.split(/\r?\n/)) {
    const kv = /^([A-Za-z0-9_.-]+):(?:\s+(.*)|\s*)$/.exec(line);
    if (kv) out.push([kv[1], (kv[2] ?? "").trim(), []]);
    else if (out.length && (/^\s/.test(line) || line === "")) out[out.length - 1][2].push(line);
    else if (line.trim() && !line.trimStart().startsWith("#")) return null;
  }
  return out.map(([k, v, more]) => {
    const lines = more.join("\n").replace(/\s+$/, "");
    const indent = Math.min(...more.filter((l) => l.trim()).map((l) => l.match(/^\s*/)![0].length));
    const block = more
      .map((l) => l.slice(Number.isFinite(indent) ? indent : 0))
      .join("\n")
      .trim();
    if (/^[|>][-+]?$/.test(v)) return [k, v.startsWith(">") ? block.replace(/\n(?!\n)/g, " ") : block];
    if (!lines) return [k, /^(["']).*\1$/.test(v) ? v.slice(1, -1) : v];
    return [k, v ? `${v} ${block.replace(/\n/g, " ")}` : block]; // plain multi-line scalar, or a list / map as written
  });
}

function FrontMatter({ raw }: { raw: string }) {
  const rows = parseFrontMatter(raw);
  return (
    <div className="not-prose mb-5 rounded-md border bg-muted/40 text-[12.5px]">
      {rows ? (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 px-3 py-2.5">
          {rows.map(([k, v], i) => (
            <div key={i} className="contents">
              <dt className="font-mono text-muted-foreground">{k}</dt>
              <dd className="min-w-0 break-words whitespace-pre-wrap">{v}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <pre className="overflow-x-auto px-3 py-2.5 font-mono text-[12px] whitespace-pre">{raw}</pre>
      )}
    </div>
  );
}

/**
 * The Apps hand the address to the system browser, which has no gerdr cookie: give it a short-lived link that needs
 * none (server/filelink.go) instead of the plain /api/fs/raw one, which would be a 401 there.
 */
async function openOutside(stat: FileStat) {
  try {
    const { url } = await api.fileLink(stat.path);
    const abs = new URL(url, location.href).href;
    if (isAndroid()) postAndroid({ t: "open", url: abs });
    else nativeOpenURL(abs);
  } catch (e) {
    toast.error((e as Error).message);
  }
}

/**
 * Phones (Chrome on Android, the Android App's WebView) have no built-in PDF viewer: an iframe there shows nothing, or
 * worse, the App's WebView treats it as a download the moment the viewer opens. Older browsers without the flag do
 * have one (desktop Safari / Chrome before it existed).
 */
const canShowPDF = () => (navigator as { pdfViewerEnabled?: boolean }).pdfViewerEnabled ?? true;

/** Whatever can't be shown here (binary, too big, a PDF on a phone) still gets a big download button, not just the header icon. */
function NoPreview({ stat, why }: { stat: FileStat; why: string }) {
  return (
    <div className="flex flex-col items-center gap-4 p-10 text-center text-[13px] text-muted-foreground">
      <div>{why}</div>
      <Button asChild>
        <a href={fileRawURL(stat.path, true)} download={stat.name}>
          <Download className="size-4" />
          {t("fileViewer.downloadSize", { size: fmtSize(stat.size) })}
        </a>
      </Button>
    </div>
  );
}
