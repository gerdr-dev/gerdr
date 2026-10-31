import { Clipboard, Copy, Download, ExternalLink, FileText, Image as ImageIcon, Scissors, TextSelect } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { androidCan, androidCopyImage, isAndroid } from "@/lib/android";
import { isNative, nativeOpenURL } from "@/lib/native";

/**
 * Right-click menu for the desktop App. Electron has no menu of its own, so a right-click did nothing: no copying a
 * selection, no copying or saving an image from the chat. It is drawn here rather than as a native Electron menu so it
 * looks like the rest of the app. A browser tab keeps the browser's own menu (spellcheck, translate, inspect).
 *
 * The Android App's WebView has no long-press menu either, so a long press on an image did nothing; there the menu
 * opens on long press (it arrives as contextmenu) for images, videos and links only. Text keeps the system's own
 * selection handles and copy bar, which work and which people expect. "Save" is left out there: WebView hands
 * downloads to the external browser (MainActivity), which isn't signed in and can't fetch the file. "Copy image"
 * goes through the shell (androidCopyImage) and only shows when the installed App says it can.
 *
 * Elements that draw their own menu (sidebar tabs, Radix ContextMenu) call preventDefault first and are left alone;
 * so are terminals, where xterm owns the mouse and its selection isn't a DOM selection.
 */

type Item = { label: string; icon: ReactNode; run: () => unknown; disabled?: boolean };
type Menu = { x: number; y: number; groups: Item[][] };

const icon = "size-3.5";

// On macOS, Chromium selects the word or element under the cursor before it fires contextmenu, so right-clicking an
// image painted it blue (user report). Remember the selection at pointerdown and put it back when the menu opens.
let saved: Range[] = [];

function restoreSelection() {
  const sel = window.getSelection();
  if (!sel) return;
  sel.removeAllRanges();
  for (const r of saved) sel.addRange(r);
}

const selectedText = () => saved.map((r) => r.toString()).join("");

// Is the click on the selected text? Right-clicking elsewhere while something is selected shouldn't offer to copy it.
const onSelection = (x: number, y: number) =>
  saved.some((r) => Array.from(r.getClientRects()).some((b) => x >= b.left && x <= b.right && y >= b.top && y <= b.bottom));

// Chat media from this machine is served as /api/fs/raw?path=…&cwd=… (ChatMedia). Someone copying it wants the file
// path, not a localhost URL that stops working when the port changes.
function localFile(src: string): string | null {
  const u = new URL(src, location.href);
  const p = u.searchParams.get("path");
  if (u.origin !== location.origin || u.pathname !== "/api/fs/raw" || !p) return null;
  const cwd = u.searchParams.get("cwd");
  return p.startsWith("/") || p.startsWith("~") || !cwd ? p : `${cwd.replace(/\/$/, "")}/${p.replace(/^\.\//, "")}`;
}

const fail = (e: unknown) => {
  toast.error(e instanceof Error ? e.message : String(e));
};

async function copyText(text: string) {
  await navigator.clipboard.writeText(text).catch(fail);
}

// The clipboard only takes PNG, so redraw whatever the image is. Same-origin media can always be drawn; a remote
// image without CORS taints the canvas and fails, which the toast says.
async function copyImage(img: HTMLImageElement) {
  try {
    if (isAndroid()) {
      // The phone's clipboard takes the file as it is (jpg, gif…); no need to redraw it as PNG
      const r = await fetch(img.currentSrc || img.src, { credentials: "same-origin" });
      if (!r.ok) throw new Error(`读不到图片（${r.status}）`);
      await androidCopyImage(await r.blob());
      // Said here, not left to the system: HyperOS shows nothing when an app copies an image
      return void toast.success("图片已复制");
    }
    const c = document.createElement("canvas");
    c.width = img.naturalWidth;
    c.height = img.naturalHeight;
    c.getContext("2d")!.drawImage(img, 0, 0);
    const blob = await new Promise<Blob>((ok, no) => c.toBlob((b) => (b ? ok(b) : no(new Error("复制不了这张图"))), "image/png"));
    await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
    toast.success("图片已复制");
  } catch (e) {
    fail(e);
  }
}

function saveFile(src: string, name: string) {
  const a = document.createElement("a");
  a.href = src;
  a.download = name;
  a.click();
}

const fileName = (src: string, alt?: string) => {
  const f = localFile(src) ?? new URL(src, location.href).pathname;
  return f.split("/").pop() || alt || "image.png";
};

function editable(el: Element | null): HTMLElement | null {
  const e = el?.closest<HTMLElement>("input, textarea, [contenteditable]:not([contenteditable=false])");
  if (!e || (e instanceof HTMLInputElement && !/^(text|search|url|email|password|number|tel|)$/.test(e.type))) return null;
  return e;
}

function buildMenu(e: MouseEvent, touch: boolean): Item[][] {
  const t = e.target instanceof Element ? e.target : null;
  const groups: Item[][] = [];
  const field = editable(t);
  if (field && !touch) {
    const ro = field.matches("[readonly], [disabled]");
    const hasSel =
      field instanceof HTMLInputElement || field instanceof HTMLTextAreaElement
        ? (field.selectionStart ?? 0) !== (field.selectionEnd ?? 0)
        : !window.getSelection()?.isCollapsed;
    const exec = (cmd: string) => () => {
      field.focus();
      document.execCommand(cmd);
    };
    groups.push([
      { label: "剪切", icon: <Scissors className={icon} />, run: exec("cut"), disabled: ro || !hasSel },
      { label: "复制", icon: <Copy className={icon} />, run: exec("copy"), disabled: !hasSel },
      {
        label: "粘贴",
        icon: <Clipboard className={icon} />,
        disabled: ro,
        // execCommand("paste") is blocked in pages; read the clipboard and type it in so undo still works
        run: async () => {
          const text = await navigator.clipboard.readText().catch(fail);
          field.focus();
          if (text) document.execCommand("insertText", false, text);
        },
      },
      { label: "全选", icon: <TextSelect className={icon} />, run: exec("selectAll") },
    ]);
    return groups;
  }
  if (!touch && selectedText().trim() && onSelection(e.clientX, e.clientY)) {
    groups.push([
      {
        label: "复制",
        icon: <Copy className={icon} />,
        // execCommand keeps the formatting (a pasted table stays a table); writeText is the fallback
        run: () => {
          restoreSelection();
          if (!document.execCommand("copy")) copyText(selectedText());
        },
      },
    ]);
  }
  const a = t?.closest<HTMLAnchorElement>("a[href]");
  if (a && /^(https?|mailto):/i.test(a.href) && new URL(a.href).origin !== location.origin) {
    const href = a.href;
    groups.push([
      { label: "在浏览器中打开链接", icon: <ExternalLink className={icon} />, run: () => nativeOpenURL(href) },
      { label: "复制链接", icon: <Copy className={icon} />, run: () => copyText(href) },
    ]);
  }
  const media = t?.closest<HTMLImageElement | HTMLVideoElement | HTMLAudioElement>("img, video, audio");
  const src = media?.currentSrc || media?.getAttribute("src");
  if (media && src) {
    const file = localFile(src);
    const isImg = media instanceof HTMLImageElement;
    groups.push([
      ...(isImg && (!touch || androidCan("copyImage")) ? [{ label: "复制图片", icon: <ImageIcon className={icon} />, run: () => copyImage(media) }] : []),
      ...(touch
        ? []
        : [
            {
              label: isImg ? "存储图片…" : "存储…",
              icon: <Download className={icon} />,
              run: () => saveFile(src, fileName(src, isImg ? media.alt : undefined)),
            },
          ]),
      file
        ? { label: "复制文件路径", icon: <FileText className={icon} />, run: () => copyText(file) }
        : { label: isImg ? "复制图片地址" : "复制地址", icon: <Copy className={icon} />, run: () => copyText(new URL(src, location.href).href) },
    ]);
  }
  return groups;
}

export function AppContextMenu() {
  const [menu, setMenu] = useState<Menu | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);

  useEffect(() => {
    const touch = isAndroid();
    if (!isNative() && !touch) return;
    const down = (e: PointerEvent) => {
      if (e.button !== 2) return;
      const sel = window.getSelection();
      saved = sel ? Array.from({ length: sel.rangeCount }, (_, i) => sel.getRangeAt(i).cloneRange()) : [];
    };
    const open = (e: MouseEvent) => {
      const t = e.target instanceof Element ? e.target : null;
      if (e.defaultPrevented || t?.closest(".xterm")) return;
      const groups = buildMenu(e, touch);
      // Nothing to offer on Android: let the long press go on to the system's text selection
      if (touch && !groups.length) return;
      e.preventDefault();
      if (!touch && !editable(t)) restoreSelection();
      setPos(null);
      setMenu(groups.length ? { x: e.clientX, y: e.clientY, groups } : null);
    };
    // Android's WebView starts dragging an image on the same long press that opens this menu. While that drag is on
    // it swallows every tap: the menu couldn't be picked over the file viewer, and afterwards even the lightbox
    // wouldn't open (user report). Nothing on the phone takes a dropped image, so images don't drag there at all.
    const noDrag = (e: DragEvent) => touch && e.target instanceof HTMLImageElement && e.preventDefault();
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("contextmenu", open);
    document.addEventListener("dragstart", noDrag, true);
    return () => {
      document.removeEventListener("pointerdown", down, true);
      document.removeEventListener("contextmenu", open);
      document.removeEventListener("dragstart", noDrag, true);
    };
  }, []);

  useEffect(() => {
    if (!menu) return;
    const close = (e: Event) => {
      if (e instanceof PointerEvent && box.current?.contains(e.target as Node)) return;
      setMenu(null);
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && setMenu(null);
    window.addEventListener("pointerdown", close, true);
    window.addEventListener("scroll", close, true);
    window.addEventListener("resize", close);
    window.addEventListener("blur", close);
    window.addEventListener("keydown", key, true);
    return () => {
      window.removeEventListener("pointerdown", close, true);
      window.removeEventListener("scroll", close, true);
      window.removeEventListener("resize", close);
      window.removeEventListener("blur", close);
      window.removeEventListener("keydown", key, true);
    };
  }, [menu]);

  // Open at the cursor, flipped back inside the window when it would run off the right or bottom edge
  useLayoutEffect(() => {
    const el = box.current;
    if (!menu || !el) return;
    const { width, height } = el.getBoundingClientRect();
    const left = menu.x + width + 4 > innerWidth ? Math.max(4, menu.x - width) : menu.x;
    const top = menu.y + height + 4 > innerHeight ? Math.max(4, menu.y - height) : menu.y;
    setPos({ left, top });
  }, [menu]);

  if (!menu) return null;
  // Above everything, the lightbox (z-200) included: right-clicking the enlarged image has to show the menu too.
  // pointer-events-auto: an open modal dialog (the file viewer) sets pointer-events: none on <body>, and this menu is
  // outside it, so without it a tap on "复制图片" went nowhere — no copy, no error (user report)
  return (
    <div
      ref={box}
      role="menu"
      data-app-context-menu
      style={pos ?? { left: menu.x, top: menu.y, visibility: "hidden" }}
      className="pointer-events-auto fixed z-[300] min-w-40 rounded-lg border bg-popover p-1 text-sm text-popover-foreground shadow-md select-none"
      onContextMenu={(e) => e.preventDefault()}
      // Keep focus and the selection where they were: clicking an item must not clear what it is about to copy
      onMouseDown={(e) => e.preventDefault()}
    >
      {menu.groups.map((g, i) => (
        <div key={i} className={i ? "mt-1 border-t pt-1" : undefined}>
          {g.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              disabled={it.disabled}
              className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50 [&_svg]:text-muted-foreground"
              onClick={() => {
                setMenu(null);
                it.run();
              }}
            >
              {it.icon}
              {it.label}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}
