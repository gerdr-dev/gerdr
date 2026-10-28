import { diffLines } from "diff";
import { useContext, useMemo, useState } from "react";
import { openPath } from "@/lib/actions";
import { cn } from "@/lib/utils";
import { SessionContext } from "./sessionContext";

type Line = { t: " " | "+" | "-" | "@"; s: string };

function fromTexts(oldText: string | null | undefined, newText: string): Line[] {
  const out: Line[] = [];
  for (const part of diffLines(oldText ?? "", newText)) {
    const lines = part.value.replace(/\n$/, "").split("\n");
    const t = part.added ? "+" : part.removed ? "-" : " ";
    for (const s of lines) out.push({ t, s });
  }
  // 大段没改的上下文折掉，只留改动前后 3 行。
  const keep = new Set<number>();
  out.forEach((l, i) => {
    if (l.t !== " ") for (let j = Math.max(0, i - 3); j <= Math.min(out.length - 1, i + 3); j++) keep.add(j);
  });
  if (keep.size === 0) return out.slice(0, 40);
  const res: Line[] = [];
  let gap = false;
  out.forEach((l, i) => {
    if (keep.has(i)) {
      res.push(l);
      gap = false;
    } else if (!gap) {
      res.push({ t: "@", s: "…" });
      gap = true;
    }
  });
  return res;
}

function fromUnified(diff: string): Line[] {
  const out: Line[] = [];
  for (const s of diff.split("\n")) {
    if (s.startsWith("+++") || s.startsWith("---") || s.startsWith("diff ") || s.startsWith("index ")) continue;
    if (s.startsWith("@@")) out.push({ t: "@", s });
    else if (s.startsWith("+")) out.push({ t: "+", s: s.slice(1) });
    else if (s.startsWith("-")) out.push({ t: "-", s: s.slice(1) });
    else if (s.startsWith("\\")) continue;
    else out.push({ t: " ", s: s.startsWith(" ") ? s.slice(1) : s });
  }
  while (out.length && out[out.length - 1].t === " " && out[out.length - 1].s === "") out.pop();
  return out;
}

export function DiffView({
  oldText,
  newText,
  unified,
  path,
  maxLines = 24,
}: {
  oldText?: string | null;
  newText?: string;
  unified?: string;
  path?: string;
  maxLines?: number;
}) {
  const lines = useMemo(() => (unified != null ? fromUnified(unified) : fromTexts(oldText, newText ?? "")), [oldText, newText, unified]);
  const [all, setAll] = useState(false);
  const session = useContext(SessionContext);
  const shown = all ? lines : lines.slice(0, maxLines);
  const adds = lines.filter((l) => l.t === "+").length;
  const dels = lines.filter((l) => l.t === "-").length;
  return (
    <div className="overflow-hidden rounded-md border bg-card font-mono text-[12px] leading-[1.55]">
      {path && (
        <div className="flex items-center justify-between border-b bg-muted/40 px-2 py-1 text-[11px] text-muted-foreground">
          {/* 点文件名看改完之后的整个文件（diff 只给了改动那几段） */}
          <button className="truncate text-left hover:text-foreground hover:underline" title="打开这个文件" onClick={() => openPath(path, session?.cwd)}>
            {path}
          </button>
          <span className="shrink-0 pl-2">
            <span className="text-ok">+{adds}</span> <span className="text-bad">−{dels}</span>
          </span>
        </div>
      )}
      <div className="overflow-x-auto">
        {shown.map((l, i) => (
          <div
            key={i}
            className={cn(
              "whitespace-pre px-2",
              l.t === "+" && "bg-ok/12 text-foreground",
              l.t === "-" && "bg-bad/12 text-foreground",
              l.t === "@" && "text-muted-foreground",
            )}
          >
            <span className={cn("inline-block w-3 select-none", l.t === "+" ? "text-ok" : l.t === "-" ? "text-bad" : "text-muted-foreground/50")}>
              {l.t === "@" ? "" : l.t === " " ? "" : l.t}
            </span>
            {l.s || " "}
          </div>
        ))}
      </div>
      {lines.length > maxLines && (
        <button className="w-full border-t py-1 text-[11px] text-muted-foreground hover:bg-muted/50" onClick={() => setAll(!all)}>
          {all ? "收起" : `还有 ${lines.length - maxLines} 行`}
        </button>
      )}
    </div>
  );
}
