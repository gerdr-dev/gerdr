/**
 * Rich-text editing of a Markdown file that is saved back as Markdown (tiptap + @tiptap/markdown), for the artifacts
 * folder (docs/dev/ARTIFACTS.md). Loaded only when a .md file is opened there (Folder.tsx imports it lazily).
 *
 * Reading a file and saving it back should not rewrite lines nobody touched, or every save of a draft the agent wrote
 * turns into a diff of the whole file. Two tweaks for that, as in the Annulo creator template's mdExtensions (the
 * same problem, met first there): tables are written compact (`| a | b |`, as people and agents write them) instead
 * of padded to the widest cell, and only characters that would really be read as formatting are escaped.
 *
 * Everything tiptap is imported from @tiptap/react (which re-exports @tiptap/core) and the extension packages, never
 * @tiptap/core directly: each package comes from esm.sh, and a second copy of core under another URL breaks it.
 */
import { EditorContent, Extension, mergeAttributes, useEditor, type Editor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import Image from "@tiptap/extension-image";
import { Table, TableCell, TableHeader, TableRow } from "@tiptap/extension-table";
import { TaskItem, TaskList } from "@tiptap/extension-list";
import { Bold, Code, Heading1, Heading2, Heading3, Italic, List, ListChecks, ListOrdered, Minus, Quote, SquareCode, Strikethrough } from "lucide-react";
import { useEffect, useMemo, useRef } from "react";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

type Json = { type?: string; attrs?: Record<string, unknown>; content?: Json[] };
type Helpers = { renderChildren: (nodes: Json | Json[], separator?: string) => string };

function compactTable(node: Json, h: Helpers) {
  const rows = (node.content ?? []).map((row) =>
    (row.content ?? []).map((cell) => ({
      text: (cell.content ?? [])
        .map((c) => h.renderChildren(c))
        .join("\n")
        .replace(/[ \t]*\r?\n[ \t]*/g, "<br>")
        .replace(/\s+/g, " ")
        .trim(),
      header: cell.type === "tableHeader",
      align: cell.attrs?.align as string | undefined,
    })),
  );
  const cols = Math.max(0, ...rows.map((r) => r.length));
  if (!cols) return "";
  const line = (cells: string[]) => `| ${Array.from({ length: cols }, (_, i) => cells[i] ?? "").join(" | ")} |`;
  const head = rows[0].some((c) => c.header) ? rows[0] : [];
  const body = head.length ? rows.slice(1) : rows;
  const align = Array.from({ length: cols }, (_, i) => rows.find((r) => r[i]?.align)?.[i]?.align);
  const sep = `|${align.map((a) => (a === "left" ? ":---" : a === "right" ? "---:" : a === "center" ? ":---:" : "---")).join("|")}|`;
  return [line(head.map((c) => c.text)), sep, ...body.map((r) => line(r.map((c) => c.text)))].join("\n");
}

/** Escapes only what would be read as formatting (the stock one backslashes every \ ` * _ [ ] ~ and turns < & into entities). */
function escapeText(text: string) {
  return text
    .replace(/\\(?=[\\`*_[\]~<&])/g, "\\\\")
    .replace(/[`*]/g, "\\$&")
    .replace(/(^|[^\p{L}\p{N}])_|_(?=$|[^\p{L}\p{N}])/gu, (m) => m.replace("_", "\\_"))
    .replace(/~~/g, "\\~\\~")
    .replace(/\[([^\]]*)\]\(/g, "\\[$1\\](")
    .replace(/<(?=[A-Za-z/!?])/g, "&lt;")
    .replace(/&(?=#?\w+;)/g, "&amp;");
}

type MdApi = {
  codeTypes: Set<string>;
  encodeTextForMarkdown: (text: string, node: { marks?: (string | { type: string })[] }, parent?: { type?: string }) => string;
};

const mdTweaks = Extension.create({
  name: "mdTweaks",
  priority: 1, // after Markdown has set up editor.markdown
  onBeforeCreate() {
    const m = (this.editor as unknown as { markdown?: MdApi }).markdown;
    if (!m) return;
    m.encodeTextForMarkdown = (text, node, parent) => {
      const inCode = (parent?.type && m.codeTypes.has(parent.type)) || (node.marks ?? []).some((x) => m.codeTypes.has(typeof x === "string" ? x : x.type));
      return inCode ? text : escapeText(text);
    };
  },
});

/**
 * value: the Markdown when mounted (remount with a key to load another version). onChange gets the Markdown after
 * every edit, and once right after mounting: the normalized form, which the caller takes as "unchanged".
 * resolveSrc: an image's src as written (often relative to the file) → a URL the page can load.
 */
export default function MdEditor({ value, onChange, resolveSrc }: { value: string; onChange: (md: string) => void; resolveSrc: (src: string) => string }) {
  const change = useRef(onChange);
  change.current = onChange;
  const resolve = useRef(resolveSrc);
  resolve.current = resolveSrc;
  const extensions = useMemo(
    () => [
      StarterKit.configure({ link: { openOnClick: false } }),
      Markdown.configure({ markedOptions: { gfm: true } }),
      mdTweaks,
      // Shown through Gerdr's file URL, written back as it was in the file
      Image.extend({
        renderHTML({ HTMLAttributes }) {
          return ["img", mergeAttributes(this.options.HTMLAttributes, { ...HTMLAttributes, src: resolve.current(String(HTMLAttributes.src ?? "")) })];
        },
      }),
      Table.extend({ renderMarkdown: (node: Json, h: Helpers) => compactTable(node, h) }).configure({ resizable: false }),
      TableRow,
      TableHeader,
      TableCell,
      TaskList,
      TaskItem.configure({ nested: true }),
    ],
    [],
  );
  const editor = useEditor({
    extensions,
    content: value,
    contentType: "markdown",
    shouldRerenderOnTransaction: true, // the toolbar's active states
    onUpdate: ({ editor }) => change.current(editor.getMarkdown()),
  });
  useEffect(() => {
    if (editor) change.current(editor.getMarkdown());
  }, [editor]);
  if (!editor) return null;
  return (
    <div className="flex min-h-full flex-col">
      <Toolbar editor={editor} />
      <EditorContent editor={editor} className="gerdr-editor mx-auto w-full max-w-[860px] flex-1 px-5 py-4" />
    </div>
  );
}

function Toolbar({ editor }: { editor: Editor }) {
  const c = () => editor.chain().focus();
  const items: { key: string; icon: typeof Bold; on: boolean; run: () => void }[] = [
    { key: "folder.bold", icon: Bold, on: editor.isActive("bold"), run: () => c().toggleBold().run() },
    { key: "folder.italic", icon: Italic, on: editor.isActive("italic"), run: () => c().toggleItalic().run() },
    { key: "folder.strike", icon: Strikethrough, on: editor.isActive("strike"), run: () => c().toggleStrike().run() },
    { key: "folder.code", icon: Code, on: editor.isActive("code"), run: () => c().toggleCode().run() },
    { key: "folder.h1", icon: Heading1, on: editor.isActive("heading", { level: 1 }), run: () => c().toggleHeading({ level: 1 }).run() },
    { key: "folder.h2", icon: Heading2, on: editor.isActive("heading", { level: 2 }), run: () => c().toggleHeading({ level: 2 }).run() },
    { key: "folder.h3", icon: Heading3, on: editor.isActive("heading", { level: 3 }), run: () => c().toggleHeading({ level: 3 }).run() },
    { key: "folder.bullet", icon: List, on: editor.isActive("bulletList"), run: () => c().toggleBulletList().run() },
    { key: "folder.ordered", icon: ListOrdered, on: editor.isActive("orderedList"), run: () => c().toggleOrderedList().run() },
    { key: "folder.task", icon: ListChecks, on: editor.isActive("taskList"), run: () => c().toggleTaskList().run() },
    { key: "folder.quote", icon: Quote, on: editor.isActive("blockquote"), run: () => c().toggleBlockquote().run() },
    { key: "folder.codeBlock", icon: SquareCode, on: editor.isActive("codeBlock"), run: () => c().toggleCodeBlock().run() },
    { key: "folder.hr", icon: Minus, on: false, run: () => c().setHorizontalRule().run() },
  ];
  return (
    <div className="sticky top-[var(--md-bar,33px)] z-10 flex flex-wrap items-center gap-0.5 border-b bg-background px-2 py-1">
      {items.map(({ key, icon: Icon, on, run }) => (
        <button
          key={key}
          type="button"
          title={t(key as "folder.bold")}
          onMouseDown={(e) => e.preventDefault()} // keep the selection in the editor
          onClick={run}
          className={cn(
            "flex size-7 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground",
            on && "bg-accent text-foreground",
          )}
        >
          <Icon className="size-4" />
        </button>
      ))}
    </div>
  );
}
