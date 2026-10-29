import { Loader2, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import type { NativeMCPEntry } from "@/lib/types";
import { cn } from "@/lib/utils";

// 设置 → MCP 页的「Gerdr Agent 在用的」：按上面选的来源算出来、它实际会用的服务器，每个来自哪份配置、连上没有。
// 只列出三份配置的话，人看不出 Gerdr Agent 最后用的是哪些（用户问的）。项目级的（.mcp.json）按会话目录另算，这里只看用户级。

const FROM: Record<string, string> = { gerdr: "gerdr 的", claude: "Claude Code 的" };

export function NativeMCP({ deps }: { deps: string }) {
  const available = useStore((s) => s.agents.some((a) => a.id === "native" && a.available));
  const [list, setList] = useState<NativeMCPEntry[] | null>(null);
  const [probing, setProbing] = useState(false);
  const load = (probe = false) => {
    if (probe) setProbing(true);
    return api
      .nativeMCP(probe)
      .then((r) => setList(r.servers))
      .catch((e) => toast.error(`查不到：${e.message}`))
      .finally(() => setProbing(false));
  };
  useEffect(() => {
    if (available) load();
  }, [available, deps]);
  if (!available) return null;

  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex items-end justify-between gap-2">
        <div>
          <div className="flex items-center gap-1.5 text-[13px] font-medium">
            <AgentIcon agent="native" className="size-3.5" /> Gerdr Agent 在用的
          </div>
          <div className="text-[12px] text-muted-foreground">按上面选的来源算出来的；第一次对话时才连，项目里的 .mcp.json 在那个项目的会话里另外加上</div>
        </div>
        <Button size="sm" variant="outline" disabled={probing} onClick={() => load(true)}>
          {probing ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />} 检查连接
        </Button>
      </div>
      <div className="divide-y rounded-md border">
        {list === null ? (
          <div className="px-3 py-2.5 text-[12px] text-muted-foreground">读取中…</div>
        ) : list.length === 0 ? (
          <div className="px-3 py-2.5 text-[12px] text-muted-foreground">一个都没有</div>
        ) : (
          list.map((m) => (
            <div key={m.name} className="space-y-0.5 px-3 py-2">
              <div className="flex items-center gap-2 text-[13px]">
                <span
                  className={cn(
                    "size-2 shrink-0 rounded-full",
                    m.status === "connected" ? "bg-ok" : m.status === "failed" ? "bg-bad" : m.status === "connecting" ? "bg-warn" : "bg-muted-foreground/40",
                  )}
                />
                <span className="font-medium">{m.name}</span>
                <span className="text-[12px] text-muted-foreground">{FROM[m.from] ?? m.from}</span>
                <span className="flex-1" />
                <span className={cn("shrink-0 text-[12px]", m.status === "failed" ? "text-bad" : "text-muted-foreground")}>
                  {m.status === "connected"
                    ? `已连上 · ${m.tools ?? 0} 个工具`
                    : m.status === "failed"
                      ? "连不上"
                      : m.status === "connecting"
                        ? "正在连"
                        : "还没连（用到时才连）"}
                </span>
              </div>
              <div className="truncate pl-4 font-mono text-[11px] text-muted-foreground">{m.target}</div>
              {m.error && <div className="pl-4 text-[12px] break-all text-bad">{m.error}</div>}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
