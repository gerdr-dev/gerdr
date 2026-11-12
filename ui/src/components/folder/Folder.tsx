import {
  ArrowLeft,
  ChevronDown,
  ChevronRight,
  File,
  FileText,
  Film,
  Folder as FolderIcon,
  FolderOpen,
  FolderPlus,
  FilePlus,
  Image as ImageIcon,
  LayoutGrid,
  List,
  MoreHorizontal,
  Music,
  Pencil,
  Pin,
  RefreshCw,
  Trash2,
  Upload,
} from "lucide-react";
import {
  Component,
  lazy,
  Suspense,
  type ErrorInfo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type DragEvent,
  type ReactNode,
} from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { api, ApiError, fileRawURL, type ArtifactsInfo, type FileStat, type FsEntry } from "@/lib/api";
import { t } from "@/lib/i18n";
import { IS_MAC } from "@/lib/keys";
import { isNative, nativeOpenPath } from "@/lib/native";
import { cn } from "@/lib/utils";
import { FileContent } from "../FileViewer";
import { Records } from "./Records";

const MdEditor = lazy(() => import("./MdEditor"));

/** Re-listed this often while shown: an agent writes into the folder while the person looks at it. */
const POLL_MS = 3000;

const join = (a: string, b: string) => (a.endsWith("/") ? a + b : `${a}/${b}`);
const parentOf = (p: string) => p.slice(0, p.lastIndexOf("/")) || "/";
const baseName = (p: string) => p.slice(p.lastIndexOf("/") + 1);
const isMd = (name: string) => /\.(md|markdown)$/i.test(name);
const fmtSize = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`);

function EntryIcon({ e, open, className }: { e: FsEntry; open?: boolean; className?: string }) {
  const kind = e.mime?.split("/")[0];
  const Icon = e.dir
    ? open
      ? FolderOpen
      : FolderIcon
    : kind === "image"
      ? ImageIcon
      : kind === "video"
        ? Film
        : kind === "audio"
          ? Music
          : kind === "text" || isMd(e.name)
            ? FileText
            : File;
  return <Icon className={cn("size-4 shrink-0", e.dir ? "text-brand" : "text-muted-foreground", className)} />;
}

/**
 * An artifacts folder (docs/dev/ARTIFACTS.md): its records on top, then its files as a tree with the selected file
 * (or folder) beside it. Markdown files are edited in place, everything else previewed like FileViewer. by: a session
 * (its folder: may not exist yet) or a folder by path. host: the folder is on another computer.
 */
export function Folder({ by, host }: { by: { session: string } | { dir: string }; host?: string }) {
  const [info, setInfo] = useState<ArtifactsInfo | null>(null);
  const [err, setErr] = useState("");
  const [lists, setLists] = useState<Record<string, FsEntry[]>>({});
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [sel, setSel] = useState<string | null>(null); // a file's or folder's path; null = the root
  const [view, setView] = useState<"list" | "grid">(() => (localStorage.getItem("gerdr.folder.view") === "grid" ? "grid" : "list"));
  const [width, setWidth] = useState(1000);
  const [creating, setCreating] = useState<"file" | "folder" | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [dropping, setDropping] = useState(false);
  const [busy, setBusy] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const upload = useRef<HTMLInputElement>(null);
  const byKey = JSON.stringify(by);

  const root = info?.exists ? info.dir : null;
  const selPath = sel ?? root;
  const entryOf = useCallback((p: string): FsEntry | undefined => lists[parentOf(p)]?.find((e) => e.name === baseName(p)), [lists]);
  const selEntry = sel ? entryOf(sel) : undefined;
  const selIsDir = !sel || !!selEntry?.dir;
  const targetDir = selPath ? (selIsDir ? selPath : parentOf(selPath)) : null;

  // What to keep listed: the root, every open folder and the selected one
  const want = useRef<string[]>([]);
  want.current = root ? [root, ...expanded, ...(sel && selIsDir ? [sel] : [])] : [];

  const reload = useCallback(async () => {
    try {
      const i = await api.artifacts(JSON.parse(byKey), host);
      setInfo((old) => (JSON.stringify(old) === JSON.stringify(i) ? old : i));
      setErr("");
      if (!i.exists) return;
      const dirs = [...new Set([i.dir, ...want.current.filter((d) => d !== i.dir)])];
      const got = await Promise.all(
        dirs.map((d) =>
          api.fsList(d, host).then(
            (r) => [d, r.entries] as const,
            () => [d, null] as const,
          ),
        ),
      );
      setLists((old) => {
        const next = { ...old };
        let changed = false;
        for (const [d, es] of got) {
          if (!es) {
            if (d in next) (delete next[d], (changed = true));
            continue;
          }
          if (JSON.stringify(next[d]) !== JSON.stringify(es)) ((next[d] = es), (changed = true));
        }
        return changed ? next : old;
      });
    } catch (e) {
      setErr((e as Error).message);
    }
  }, [byKey, host]);

  useEffect(() => {
    setInfo(null);
    setLists({});
    setSel(null);
    setExpanded(new Set());
  }, [byKey, host]);
  useEffect(() => {
    void reload();
    const tick = () => document.visibilityState === "visible" && void reload();
    const id = setInterval(tick, POLL_MS);
    return () => clearInterval(id);
  }, [reload]);
  useEffect(() => void reload(), [expanded, sel, reload]);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const narrow = width < 640;

  const toggle = (p: string) =>
    setExpanded((s) => {
      const n = new Set(s);
      if (n.has(p)) n.delete(p);
      else n.add(p);
      return n;
    });
  const pick = (p: string, dir: boolean) => {
    setSel(p);
    if (dir) setExpanded((s) => new Set(s).add(p));
  };
  const openRel = (rel: string) => {
    if (!root) return;
    const p = join(root, rel);
    let d = parentOf(p);
    const open = new Set(expanded);
    while (d.length > root.length) (open.add(d), (d = parentOf(d)));
    setExpanded(open);
    setSel(p);
  };

  const ensure = async () => {
    if (!info) return;
    setBusy(true);
    try {
      await api.fsMkdir(info.dir, host);
      await reload();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const putFiles = async (files: File[], dir: string) => {
    let n = 0;
    for (const f of files) {
      const dot = f.name.lastIndexOf(".");
      const stem = dot > 0 ? f.name.slice(0, dot) : f.name;
      const ext = dot > 0 ? f.name.slice(dot) : "";
      for (let i = 1; i < 50; i++) {
        const name = i === 1 ? f.name : `${stem} ${i}${ext}`;
        try {
          await api.fsPut(join(dir, name), f, "none", host);
          n++;
          break;
        } catch (e) {
          if (!(e instanceof ApiError && e.status === 412)) {
            toast.error((e as Error).message);
            break;
          }
        }
      }
    }
    if (n) toast(t("folder.uploaded", { n }));
    setExpanded((s) => new Set(s).add(dir));
    await reload();
  };
  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDropping(false);
    const files = [...e.dataTransfer.files];
    if (!files.length) return;
    void (async () => {
      if (!root && info) await api.fsMkdir(info.dir, host).catch(() => {});
      await putFiles(files, targetDir ?? info!.dir);
    })();
  };

  const create = async (name: string) => {
    const kind = creating;
    setCreating(null);
    if (!name.trim() || !targetDir) return;
    const p = join(targetDir, name.trim());
    try {
      if (kind === "folder") await api.fsMkdir(p, host);
      else await api.fsPut(p, "", "none", host);
      setExpanded((s) => new Set(s).add(targetDir));
      setSel(p);
      await reload();
    } catch (e) {
      toast.error(e instanceof ApiError && e.status === 412 ? t("folder.exists", { name: name.trim() }) : (e as Error).message);
    }
  };
  const rename = async (p: string, name: string) => {
    setRenaming(null);
    if (!name.trim() || name.trim() === baseName(p)) return;
    const to = join(parentOf(p), name.trim());
    try {
      await api.fsMove(p, to, host);
      if (sel === p) setSel(to);
      await reload();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  const trash = async (p: string) => {
    try {
      await api.fsTrash(p, host);
      if (sel && (sel === p || sel.startsWith(p + "/"))) setSel(null);
      toast(t("folder.deleted"));
      await reload();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  const pin = (p: string) =>
    root &&
    api
      .recordArtifact(by, { kind: "file", path: p.slice(root.length + 1) }, host)
      .then(reload)
      .catch((e) => toast.error(e.message));

  const actions = (p: string, e: FsEntry) => (
    <EntryMenu
      onRename={() => setRenaming(p)}
      onTrash={() => trash(p)}
      onPin={e.dir ? undefined : () => pin(p)}
      onReveal={isNative() && !host ? () => nativeOpenPath(p, undefined, true) : undefined}
    />
  );

  const toolbar = (
    <div className="flex shrink-0 items-center gap-0.5 border-b px-2 py-1">
      <span className="min-w-0 flex-1 truncate px-1 font-mono text-[11px] text-muted-foreground" title={info?.dir}>
        {info?.dir.replace(/^\/(?:Users|home)\/[^/]+/, "~")}
      </span>
      {root && (
        <>
          <Tool title={t("folder.newFile")} onClick={() => setCreating("file")}>
            <FilePlus />
          </Tool>
          <Tool title={t("folder.newFolder")} onClick={() => setCreating("folder")}>
            <FolderPlus />
          </Tool>
          <Tool title={t("folder.upload")} onClick={() => upload.current?.click()}>
            <Upload />
          </Tool>
          {isNative() && !host && (
            <Tool title={IS_MAC ? t("folder.reveal") : t("folder.revealOther")} onClick={() => nativeOpenPath(selPath ?? root, undefined, true)}>
              <FolderOpen />
            </Tool>
          )}
          <Tool title={t("folder.refresh")} onClick={() => void reload()}>
            <RefreshCw />
          </Tool>
          {!narrow && (
            <Tool
              title={view === "list" ? t("folder.grid") : t("folder.list")}
              onClick={() => {
                const v = view === "list" ? "grid" : "list";
                setView(v);
                localStorage.setItem("gerdr.folder.view", v);
              }}
            >
              {view === "list" ? <LayoutGrid /> : <List />}
            </Tool>
          )}
        </>
      )}
      <input
        ref={upload}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          const fs = [...(e.target.files ?? [])];
          e.target.value = "";
          if (fs.length && targetDir) void putFiles(fs, targetDir);
        }}
      />
    </div>
  );

  const tree = root && (
    <div className="min-h-0 overflow-auto py-1">
      {creating && targetDir === root && <NameInput depth={0} folder={creating === "folder"} onDone={create} />}
      <Tree
        dir={root}
        depth={0}
        lists={lists}
        expanded={expanded}
        sel={sel}
        renaming={renaming}
        creatingIn={creating ? targetDir : null}
        creatingFolder={creating === "folder"}
        onToggle={toggle}
        onPick={pick}
        onCreate={create}
        onRename={rename}
        actions={actions}
      />
    </div>
  );

  let right: ReactNode = null;
  if (root && selPath) {
    if (!selIsDir && selEntry) right = <FileView key={selPath} path={selPath} entry={selEntry} host={host} />;
    else if (selIsDir)
      right = <Listing dir={selPath} entries={lists[selPath]} view={narrow ? "list" : view} host={host} onOpen={(p, d) => pick(p, d)} actions={actions} />;
  }

  return (
    <div
      ref={box}
      className="relative flex h-full min-h-0 flex-col"
      onDragOver={(e) => {
        if (![...e.dataTransfer.types].includes("Files")) return;
        e.preventDefault();
        setDropping(true);
      }}
      onDragLeave={(e) => !box.current?.contains(e.relatedTarget as Node) && setDropping(false)}
      onDrop={onDrop}
    >
      {toolbar}
      {info && (
        <Records
          items={info.items}
          by={by}
          host={host}
          onChanged={() => void reload()}
          onOpenFile={openRel}
          onBrowse={(p) =>
            root && p.startsWith(root + "/") ? openRel(p.slice(root.length + 1)) : import("@/lib/actions").then((a) => a.openFolderPane({ dir: p }, host))
          }
        />
      )}
      {err && <div className="px-3 py-2 text-[12px] text-bad">{err}</div>}
      {!info && !err && <div className="py-16 text-center text-[13px] text-muted-foreground">…</div>}
      {info && !info.exists && (
        <div className="mx-auto flex max-w-sm flex-col items-center gap-3 px-6 py-14 text-center">
          <FolderIcon className="size-8 text-muted-foreground" />
          <div className="text-[14px] font-medium">{t("folder.notYet")}</div>
          <div className="text-[12px] leading-relaxed text-muted-foreground">{t("folder.notYetHint")}</div>
          <div className="font-mono text-[11px] break-all text-muted-foreground">{info.dir.replace(/^\/(?:Users|home)\/[^/]+/, "~")}</div>
          <Button size="sm" variant="outline" disabled={busy} onClick={ensure}>
            {t("folder.create")}
          </Button>
        </div>
      )}
      {root &&
        (narrow ? (
          <div className="flex min-h-0 flex-1 flex-col">
            {sel && !selIsDir ? (
              <>
                <button
                  type="button"
                  onClick={() => setSel(parentOf(sel) === root ? null : parentOf(sel))}
                  className="flex shrink-0 items-center gap-1 border-b px-3 py-1.5 text-[12px] text-muted-foreground hover:text-foreground"
                >
                  <ArrowLeft className="size-3.5" /> {t("folder.back")}
                  <span className="ml-1 truncate font-medium text-foreground">{baseName(sel)}</span>
                </button>
                <div className="min-h-0 flex-1 overflow-auto">{right}</div>
              </>
            ) : (
              tree
            )}
          </div>
        ) : (
          <div className="grid min-h-0 flex-1 grid-cols-[minmax(180px,30%)_1fr]">
            <div className="flex min-h-0 flex-col border-r">{tree}</div>
            <div className="min-h-0 overflow-auto">{right}</div>
          </div>
        ))}
      {dropping && (
        <div className="pointer-events-none absolute inset-1 flex items-center justify-center rounded-md border-2 border-dashed border-brand bg-brand/5 text-[13px] text-brand">
          {t("folder.dropHere", { dir: baseName(targetDir ?? info?.dir ?? "") })}
        </div>
      )}
    </div>
  );
}

function Tool({ title, onClick, children }: { title: string; onClick: () => void; children: ReactNode }) {
  return (
    <Button variant="ghost" size="icon" className="size-7 shrink-0 [&_svg]:size-3.5" title={title} onClick={onClick}>
      {children}
    </Button>
  );
}

function EntryMenu({ onRename, onTrash, onPin, onReveal }: { onRename: () => void; onTrash: () => void; onPin?: () => void; onReveal?: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          className="flex size-5 shrink-0 items-center justify-center rounded-sm opacity-0 group-hover:opacity-100 hover:bg-accent data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100"
        >
          <MoreHorizontal className="size-3.5" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onCloseAutoFocus={(e) => e.preventDefault()} onClick={(e) => e.stopPropagation()}>
        {onPin && (
          <DropdownMenuItem onClick={onPin}>
            <Pin /> {t("folder.pin")}
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onClick={onRename}>
          <Pencil /> {t("folder.rename")}
        </DropdownMenuItem>
        {onReveal && (
          <DropdownMenuItem onClick={onReveal}>
            <FolderOpen /> {IS_MAC ? t("folder.reveal") : t("folder.revealOther")}
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem className="text-bad focus:text-bad [&_svg]:text-bad" onClick={onTrash}>
          <Trash2 /> {t("folder.delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function NameInput({ depth, folder, initial = "", onDone }: { depth: number; folder?: boolean; initial?: string; onDone: (name: string) => void }) {
  const [v, setV] = useState(initial);
  const done = useRef(false);
  const finish = (name: string) => {
    if (done.current) return;
    done.current = true;
    onDone(name);
  };
  return (
    <div className="flex items-center gap-1.5 px-2 py-0.5" style={{ paddingLeft: 8 + depth * 14 + 14 }}>
      {folder ? <FolderIcon className="size-4 shrink-0 text-brand" /> : <File className="size-4 shrink-0 text-muted-foreground" />}
      <input
        autoFocus
        value={v}
        placeholder={t("folder.namePh")}
        onFocus={(e) => {
          const dot = initial.lastIndexOf(".");
          e.target.setSelectionRange(0, dot > 0 ? dot : initial.length);
        }}
        onChange={(e) => setV(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") finish(v);
          if (e.key === "Escape") finish("");
        }}
        onBlur={() => finish(v)}
        className="h-6 min-w-0 flex-1 rounded-sm border bg-background px-1.5 text-[12px] outline-none focus:border-brand"
      />
    </div>
  );
}

function Tree(props: {
  dir: string;
  depth: number;
  lists: Record<string, FsEntry[]>;
  expanded: Set<string>;
  sel: string | null;
  renaming: string | null;
  creatingIn: string | null;
  creatingFolder: boolean;
  onToggle: (p: string) => void;
  onPick: (p: string, dir: boolean) => void;
  onCreate: (name: string) => void;
  onRename: (p: string, name: string) => void;
  actions: (p: string, e: FsEntry) => ReactNode;
}) {
  const { dir, depth, lists, expanded, sel } = props;
  const es = lists[dir];
  if (!es) return null;
  if (!es.length && depth === 0 && props.creatingIn !== dir)
    return <div className="px-4 py-6 text-center text-[12px] text-muted-foreground">{t("folder.empty")}</div>;
  return (
    <>
      {es.map((e) => {
        const p = join(dir, e.name);
        const open = e.dir && expanded.has(p);
        if (props.renaming === p) return <NameInput key={p} depth={depth} folder={e.dir} initial={e.name} onDone={(n) => props.onRename(p, n)} />;
        return (
          <div key={p}>
            <div
              onClick={() => (e.dir && sel === p ? props.onToggle(p) : props.onPick(p, e.dir))}
              className={cn(
                "group flex cursor-default items-center gap-1.5 py-[3px] pr-2 text-[13px] hover:bg-accent/60",
                sel === p && "bg-accent text-foreground",
              )}
              style={{ paddingLeft: 8 + depth * 14 }}
            >
              {e.dir ? (
                <button
                  type="button"
                  className="flex size-3.5 shrink-0 items-center justify-center text-muted-foreground"
                  onClick={(ev) => (ev.stopPropagation(), props.onToggle(p))}
                >
                  {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                </button>
              ) : (
                <span className="w-3.5 shrink-0" />
              )}
              <EntryIcon e={e} open={open} />
              <span className="min-w-0 flex-1 truncate">{e.name}</span>
              {props.actions(p, e)}
            </div>
            {open && (
              <>
                {props.creatingIn === p && <NameInput depth={depth + 1} folder={props.creatingFolder} onDone={props.onCreate} />}
                <Tree {...props} dir={p} depth={depth + 1} />
              </>
            )}
          </div>
        );
      })}
    </>
  );
}

function Listing({
  dir,
  entries,
  view,
  host,
  onOpen,
  actions,
}: {
  dir: string;
  entries?: FsEntry[];
  view: "list" | "grid";
  host?: string;
  onOpen: (p: string, dir: boolean) => void;
  actions: (p: string, e: FsEntry) => ReactNode;
}) {
  if (!entries) return null;
  if (!entries.length) return <div className="py-16 text-center text-[13px] text-muted-foreground">{t("folder.empty")}</div>;
  if (view === "grid")
    return (
      <div className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-3 p-3">
        {entries.map((e) => {
          const p = join(dir, e.name);
          const img = e.mime?.startsWith("image/");
          return (
            <div
              key={p}
              onClick={() => onOpen(p, e.dir)}
              className="group flex cursor-default flex-col overflow-hidden rounded-md border hover:border-brand/60"
            >
              <div className="flex aspect-[4/3] items-center justify-center bg-muted/40">
                {img ? (
                  <img src={`${fileRawURL(p, false, host)}&v=${e.mtime}-${e.size}`} alt="" loading="lazy" className="max-h-full max-w-full object-contain" />
                ) : (
                  <EntryIcon e={e} className="size-8" />
                )}
              </div>
              <div className="flex items-center gap-1 px-2 py-1 text-[12px]">
                <span className="min-w-0 flex-1 truncate">{e.name}</span>
                {actions(p, e)}
              </div>
            </div>
          );
        })}
      </div>
    );
  return (
    <div className="py-1">
      {entries.map((e) => {
        const p = join(dir, e.name);
        return (
          <div key={p} onClick={() => onOpen(p, e.dir)} className="group flex cursor-default items-center gap-2 px-4 py-1.5 text-[13px] hover:bg-accent/60">
            <EntryIcon e={e} />
            <span className="min-w-0 flex-1 truncate">{e.name}</span>
            {!e.dir && <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">{fmtSize(e.size)}</span>}
            {actions(p, e)}
          </div>
        );
      })}
    </div>
  );
}

/** The selected file: Markdown editable, the rest as the file viewer draws it. */
function FileView({ path, entry, host }: { path: string; entry: FsEntry; host?: string }) {
  const stat = useMemo<FileStat>(
    () => ({ path, name: entry.name, dir: false, size: entry.size, mtime: entry.mtime, mime: entry.mime || "application/octet-stream", host }),
    [path, entry.name, entry.size, entry.mtime, entry.mime, host],
  );
  if (isMd(entry.name)) return <MdFile path={path} mtime={entry.mtime} host={host} />;
  return <FileContent key={`${entry.mtime}-${entry.size}`} stat={stat} fill />;
}

const FRONT = /^﻿?---[ \t]*\r?\n[\s\S]*?\r?\n---[ \t]*(?:\r?\n|$)/;

/**
 * A Markdown file, rich text by default (MdEditor) or its source. The --- header (title, tags of copy to publish)
 * stays out of the rich editor, which would read it as a rule and a heading: kept as is and joined back on save,
 * edited in Source. Saved only over the version it read (sha256): if the agent changed it meanwhile, an unedited
 * file just reloads, an edited one asks.
 */
function MdFile({ path, mtime, host }: { path: string; mtime: number; host?: string }) {
  const [mode, setMode] = useState<"rich" | "source">("rich");
  const [loaded, setLoaded] = useState<{ text: string; sha: string; ver: number } | null>(null);
  const [draft, setDraft] = useState<string | null>(null); // the whole file as edited; null = unchanged
  const [baseline, setBaseline] = useState<string | null>(null); // the rich editor's normalized body when loaded
  const [state, setState] = useState<"" | "saving" | "saved" | "conflict">("");
  const [err, setErr] = useState("");
  const sha = useRef("");
  const [barH, setBarH] = useState(33); // the sticky bar's height: MdEditor's toolbar sticks under it (--md-bar)
  const barRO = useRef<ResizeObserver | null>(null);
  const barRef = useCallback((el: HTMLDivElement | null) => {
    barRO.current?.disconnect();
    if (!el) return;
    barRO.current = new ResizeObserver(() => setBarH(el.offsetHeight));
    barRO.current.observe(el);
  }, []);
  const lastMd = useRef<string | null>(null); // the rich editor's latest Markdown, the new baseline after a save

  const load = useCallback(async () => {
    try {
      const st = await api.fileHash(path, host);
      const res = await fetch(`${fileRawURL(path, false, host)}&v=${st.mtime}-${st.size}`, { credentials: "same-origin" });
      if (!res.ok) throw new Error(res.statusText);
      const text = await res.text();
      sha.current = st.sha256 ?? "";
      setLoaded((old) => ({ text, sha: sha.current, ver: (old?.ver ?? 0) + 1 }));
      setDraft(null);
      setBaseline(null);
      setState("");
      setErr("");
    } catch (e) {
      setErr(t("folder.readFailed", { error: (e as Error).message }));
    }
  }, [path, host]);
  useEffect(() => void load(), [load]);

  // Changed on disk (the listing's mtime moved): reload when untouched, ask when edited; our own save is no change
  const dirty = draft !== null;
  const seen = useRef(mtime);
  useEffect(() => {
    if (mtime === seen.current) return;
    seen.current = mtime;
    void api.fileHash(path, host).then((st) => {
      if (st.sha256 === sha.current) return;
      if (dirty) setState("conflict");
      else void load();
    });
  }, [mtime]); // eslint-disable-line react-hooks/exhaustive-deps

  const front = loaded?.text.match(FRONT)?.[0] ?? "";
  const body = loaded ? loaded.text.slice(front.length) : "";

  const save = async (force = false) => {
    if (draft === null) return;
    setState("saving");
    try {
      const r = await api.fsPut(path, draft, force ? "*" : sha.current, host);
      sha.current = r.sha256;
      setLoaded((old) => (old ? { ...old, text: draft, sha: r.sha256 } : old));
      setDraft(null);
      setBaseline(mode === "rich" && lastMd.current !== null ? lastMd.current.trimEnd() : null);
      setState("saved");
      if (force) toast(t("folder.overwritten"));
    } catch (e) {
      if (e instanceof ApiError && e.status === 412) setState("conflict");
      else (setState(""), toast.error((e as Error).message));
    }
  };

  if (err) return <div className="p-6 text-center text-[13px] text-muted-foreground">{err}</div>;
  if (!loaded) return null;
  return (
    <div
      className="flex min-h-full flex-col"
      style={{ "--md-bar": `${barH}px` } as CSSProperties}
      data-own-keys="meta+s ctrl+s"
      onKeyDown={(e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === "s") {
          e.preventDefault();
          void save();
        }
      }}
    >
      <div ref={barRef} className="sticky top-0 z-20 shrink-0 bg-background">
        <div className="flex items-center gap-2 border-b px-3 py-1">
          <div className={cn("flex rounded-md bg-muted p-0.5 text-[12px]", dirty && "pointer-events-none opacity-50")}>
            {(["rich", "source"] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setMode(m)}
                className={cn("rounded-sm px-2 py-0.5 text-muted-foreground", mode === m && "bg-background text-foreground shadow-xs")}
              >
                {t(m === "rich" ? "folder.rich" : "folder.source")}
              </button>
            ))}
          </div>
          <div className="flex-1" />
          <span className="text-[11px] text-muted-foreground">
            {state === "saving" ? t("folder.saving") : dirty ? t("folder.unsaved") : state === "saved" ? t("folder.saved") : ""}
          </span>
          <Button size="sm" className="h-6 px-2.5 text-[12px]" disabled={!dirty || state === "saving"} onClick={() => void save()}>
            {t("folder.save")}
          </Button>
        </div>
        {state === "conflict" && (
          <div className="flex flex-wrap items-center gap-2 border-b bg-warn/10 px-3 py-1.5 text-[12px]">
            <span>{t("folder.changedOutside")}</span>
            <div className="flex-1" />
            <Button size="sm" variant="outline" className="h-6 px-2 text-[12px]" onClick={() => void load()}>
              {t("folder.loadNew")}
            </Button>
            <Button size="sm" variant="ghost" className="h-6 px-2 text-[12px]" onClick={() => void save(true)}>
              {t("folder.keepMine")}
            </Button>
          </div>
        )}
      </div>
      {mode === "source" ? (
        <textarea
          key={`src-${loaded.ver}`}
          defaultValue={loaded.text}
          spellCheck={false}
          onChange={(e) => setDraft(e.target.value === loaded.text ? null : e.target.value)}
          className="min-h-[60vh] w-full flex-1 resize-none bg-transparent p-4 font-mono text-[12.5px] leading-relaxed outline-none"
        />
      ) : (
        <>
          {front && (
            <pre
              title={t("folder.frontHint")}
              className="mx-auto mt-3 w-[calc(100%-2.5rem)] max-w-[820px] overflow-x-auto rounded-md bg-muted/60 px-3 py-2 font-mono text-[11px] text-muted-foreground"
            >
              {front.trim()}
            </pre>
          )}
          <Suspense fallback={null}>
            <MdErrorBoundary>
              <MdEditor
                key={`rich-${loaded.ver}`}
                value={body}
                resolveSrc={(src) =>
                  /^[a-z][a-z0-9+.-]*:|^\/\//i.test(src) ? src : fileRawURL(src.startsWith("/") ? src : join(parentOf(path), decodeURI(src)), false, host)
                }
                onChange={(md) => {
                  lastMd.current = md;
                  // The first call is the editor's normalized form of what was read: that is "unchanged"
                  if (baseline === null) return setBaseline(md.trimEnd());
                  const text = (front ? front.replace(/\n*$/, "\n\n") : "") + (md.trim() ? md.replace(/\n*$/, "\n") : "");
                  setDraft(md.trimEnd() === baseline ? null : text);
                }}
              />
            </MdErrorBoundary>
          </Suspense>
        </>
      )}
    </div>
  );
}

/** tiptap comes from esm.sh: if it fails to load, say so and leave Source usable. */
class MdErrorBoundary extends Component<{ children: ReactNode }, { error: string }> {
  state = { error: "" };
  static getDerivedStateFromError(e: Error) {
    return { error: e.message };
  }
  componentDidCatch(e: Error, info: ErrorInfo) {
    console.error("md editor", e, info);
  }
  render() {
    if (this.state.error) return <div className="p-6 text-[13px] text-muted-foreground">{t("folder.mdLoadFailed", { error: this.state.error })}</div>;
    return this.props.children;
  }
}
