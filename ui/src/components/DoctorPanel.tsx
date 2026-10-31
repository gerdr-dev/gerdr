import { RefreshCw, Stethoscope } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { focusSession } from "@/lib/actions";
import { api, type DoctorCheck, type DoctorFix, type DoctorLevel, type DoctorReport } from "@/lib/api";
import { closeDialog, get, set, setConfig, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

/*
 * Settings → Labs → Diagnosis (backend: internal/doctor). Checks that agents get the PATH the user has in their
 * terminal and that the claude / codex gerdr runs actually start. Fixes gerdr can make safely are buttons
 * (re-read PATH, pick which install to run); anything touching the system (deleting a stale install, sudo) is
 * handed to an agent with the report, so the user approves each command.
 *
 * The backend runs a light check every few minutes (no binaries executed) and pushes it over the socket;
 * the sidebar shows a red dot by Settings when it's "bad".
 */

const dot: Record<DoctorLevel, string> = { ok: "bg-ok", info: "bg-muted-foreground/40", warn: "bg-warn", bad: "bg-bad" };

const groups: { id: DoctorCheck["group"]; title: string }[] = [
  { id: "path", title: "环境" },
  { id: "agent", title: "Agent 程序" },
  { id: "tool", title: "常用命令" },
  { id: "usage", title: "额度" },
];

export function DoctorPanel() {
  const report = useStore((s) => s.doctor);
  const pinned = useStore((s) => s.config.agentBins);
  const [busy, setBusy] = useState<string | null>(null);

  const run = async (deep: boolean) => {
    setBusy("run");
    try {
      set({ doctor: await api.doctorRun(deep) });
    } catch (e: any) {
      toast.error(`诊断失败：${e.message ?? e}`);
    } finally {
      setBusy(null);
    }
  };

  const fix = async (f: DoctorFix) => {
    setBusy(f.kind + (f.path ?? ""));
    try {
      if (f.kind === "useBin" && f.agent && f.path) {
        await pinBin(f.agent, f.path);
        set({ doctor: await api.doctorRun(!!report?.deep) });
        toast.success("改好了：之后新开的会话用这一份，已经开着的会话要重开");
      } else {
        set({ doctor: await api.doctorFix(f) });
        toast.success("PATH 重新读好了：之后新开的会话生效，已经开着的会话要重开");
      }
    } catch (e: any) {
      toast.error(`没修好：${e.message ?? e}`);
    } finally {
      setBusy(null);
    }
  };

  const unpin = async (agent: string) => {
    setBusy("unpin" + agent);
    try {
      await pinBin(agent, "");
      set({ doctor: await api.doctorRun(!!report?.deep) });
    } finally {
      setBusy(null);
    }
  };

  const problems = report?.checks.filter((c) => c.level === "warn" || c.level === "bad") ?? [];

  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5 text-[13px]">
            <Stethoscope className="size-3.5" /> 诊断
          </div>
          <div className="text-[12px] text-muted-foreground">
            agent 拿到的 PATH 和终端里是不是一样、claude / codex 能不能启动。完整诊断会实际跑一下找到的每一份程序，被 macOS
            拦掉的那份会再弹一次系统提示，点「完成」就行
          </div>
        </div>
        <Button size="sm" variant="outline" className="shrink-0" disabled={!!busy} onClick={() => run(true)}>
          <RefreshCw className={cn("size-3.5", busy === "run" && "animate-spin")} /> 运行诊断
        </Button>
      </div>

      {!report ? (
        <div className="text-[12px] text-muted-foreground">还没诊断过</div>
      ) : (
        <>
          <div className="text-[11px] text-muted-foreground tabular-nums">
            {new Date(report.at).toLocaleString([], { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}{" "}
            {report.deep ? "完整诊断" : "后台快速检查（没跑程序）"}
            {problems.length > 0 && <span className={report.level === "bad" ? "text-bad" : "text-warn"}> · {problems.length} 个问题</span>}
          </div>
          {groups.map((g) => {
            const list = report.checks.filter((c) => c.group === g.id);
            if (!list.length) return null;
            return (
              <div key={g.id} className="min-w-0 space-y-1">
                <div className="text-[12px] font-medium text-muted-foreground">{g.title}</div>
                <div className="divide-y rounded-md border">
                  {list.map((c) => (
                    <CheckRow
                      key={c.id}
                      c={c}
                      compact={g.id === "tool"}
                      busy={busy}
                      pinned={c.group === "agent" ? pinned?.[c.id.replace("agent:", "")] : undefined}
                      onFix={fix}
                      onUnpin={() => unpin(c.id.replace("agent:", ""))}
                    />
                  ))}
                </div>
              </div>
            );
          })}
          {problems.length > 0 && (
            <div className="flex items-center justify-between gap-4 rounded-md border border-dashed p-3">
              <div className="text-[12px] text-muted-foreground">上面的按钮修不了的（删掉旧版本、要 sudo 的），交给 agent：它会先把要跑的命令给你看</div>
              <Button size="sm" className="shrink-0" disabled={!!busy} onClick={() => handOff(report)}>
                交给 agent 修
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function CheckRow({
  c,
  compact,
  busy,
  pinned,
  onFix,
  onUnpin,
}: {
  c: DoctorCheck;
  compact: boolean;
  busy: string | null;
  pinned?: string;
  onFix: (f: DoctorFix) => void;
  onUnpin: () => void;
}) {
  if (compact && c.level !== "bad" && !c.detail) {
    return (
      <div className="flex min-w-0 items-center gap-2 px-3 py-1.5 text-[12px]">
        <span className={cn("size-1.5 shrink-0 rounded-full", dot[c.level])} />
        <span className="w-14 shrink-0 font-mono">{c.title}</span>
        <span className="truncate font-mono text-[11px] text-muted-foreground" title={c.summary}>
          {c.summary}
        </span>
      </div>
    );
  }
  return (
    <div className="min-w-0 space-y-1.5 px-3 py-2 text-[12px]">
      <div className="flex min-w-0 items-start gap-2">
        <span className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", dot[c.level])} />
        <span className={cn("shrink-0 font-medium", compact && "font-mono")}>{c.title}</span>
        <span className={cn("min-w-0 break-all", c.level === "bad" ? "text-bad" : c.level === "warn" ? "text-warn" : "text-muted-foreground")}>
          {c.summary}
        </span>
      </div>
      {c.detail && <pre className="ml-3.5 font-mono text-[11px] break-all whitespace-pre-wrap text-muted-foreground">{c.detail}</pre>}
      {!!c.installs?.length && (c.installs.length > 1 || c.installs.some((i) => i.error || i.blocked)) && (
        <div className="ml-3.5 space-y-0.5">
          {c.installs.map((i) => (
            <div key={i.path} className="flex min-w-0 items-center gap-1.5 text-[11px]">
              {i.current && <span className="shrink-0 rounded-sm bg-brand/15 px-1 text-brand">在用</span>}
              {i.blocked && <span className="shrink-0 rounded-sm bg-bad/15 px-1 text-bad">被系统拦截</span>}
              {i.version && <span className="shrink-0 tabular-nums">{i.version}</span>}
              <span className="truncate font-mono text-muted-foreground" title={i.path}>
                {i.path}
              </span>
              {i.error && !i.blocked && <span className="shrink-0 text-bad">{i.error}</span>}
            </div>
          ))}
        </div>
      )}
      {pinned && (
        <div className="ml-3.5 flex min-w-0 items-center gap-2 text-[11px] text-muted-foreground">
          <span className="truncate">
            设置里指定了：<span className="font-mono">{pinned}</span>
          </span>
          <Button size="sm" variant="ghost" className="h-6 shrink-0 px-2 text-[11px]" disabled={!!busy} onClick={onUnpin}>
            恢复按 PATH 找
          </Button>
        </div>
      )}
      {!!c.fixes?.length && (
        <div className="ml-3.5 flex flex-wrap gap-1.5">
          {c.fixes.map((f) => (
            <Button
              key={f.kind + (f.path ?? "")}
              size="sm"
              variant="outline"
              className="h-6 px-2 text-[11px]"
              disabled={!!busy}
              title={f.path}
              onClick={() => onFix(f)}
            >
              {busy === f.kind + (f.path ?? "") && <RefreshCw className="size-3 animate-spin" />}
              {f.label}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}

// The backend reads agentBins when config.json changes (cmd/gerdr's applyBins); give the save a moment to land.
async function pinBin(agent: string, path: string) {
  const cur = { ...get().config.agentBins };
  if (path) cur[agent] = path;
  else delete cur[agent];
  setConfig({ agentBins: cur });
  await new Promise((r) => setTimeout(r, 800));
}

function reportText(r: DoctorReport) {
  const lines = [
    "Gerdr 的诊断（设置 → 实验室 → 诊断）发现了下面的问题，请帮我查清原因并修好。",
    "",
    "- 要用 sudo、删除或改动系统文件的命令，先把命令和理由告诉我，等我同意再执行。",
    "- Gerdr 自己的修复（重新读取 PATH、指定用哪一份 claude / codex）在 设置 → 实验室 → 诊断 里有按钮，需要时告诉我去点。",
    "- 你这个会话也是 Gerdr 起的，PATH 可能同样缺目录：找不到命令时用绝对路径，或者在 `zsh -lic '…'` 里跑。",
    "",
  ];
  for (const c of r.checks) {
    if (c.level !== "warn" && c.level !== "bad") continue;
    lines.push(`## ${c.title}（${c.level === "bad" ? "严重" : "注意"}）`, "", c.summary);
    if (c.detail) lines.push("", "```", c.detail, "```");
    if (c.installs?.length) {
      lines.push("", "找到的几份：");
      for (const i of c.installs) {
        const tags = [i.current && "Gerdr 在用", i.version, i.blocked && "启动就被系统杀掉", !i.blocked && i.error].filter(Boolean).join("，");
        lines.push(`- \`${i.path}\`${tags ? `（${tags}）` : ""}`);
      }
    }
    lines.push("");
  }
  return lines.join("\n");
}

// A fresh session in its own tab, started in the home dir, with the report as the first message.
// Skips an agent whose own check is "bad" (handing a broken codex the job of fixing codex goes nowhere).
async function handOff(r: DoctorReport) {
  const st = get();
  const broken = new Set(r.checks.filter((c) => c.group === "agent" && c.level === "bad").map((c) => c.id.replace("agent:", "")));
  const ok = (id?: string) => !!id && !broken.has(id) && st.agents.some((a) => a.id === id && a.available);
  const agent = [st.config.defaultAgent, "claude", "codex"].find(ok) ?? st.agents.find((a) => a.available && !broken.has(a.id))?.id;
  if (!agent) {
    toast.error("没有能用的 agent 可以交给它");
    return;
  }
  try {
    const s = await api.createSession({ agent, cwd: st.home, title: "诊断修复" });
    set((x) => ({ sessions: { ...x.sessions, [s.id]: s } }));
    closeDialog();
    focusSession(s.id);
    await api.send(s.id, [{ type: "text", text: reportText(r) }]);
  } catch (e: any) {
    toast.error(`没交出去：${e.message ?? e}`);
  }
}
