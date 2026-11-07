import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { candidates, type Cand } from "./AgentSettings";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { get, set, setConfig, useStore, type ProjectConf } from "@/lib/store";
import type { ExtView } from "@/lib/types";

/**
 * What a project switches off (config.json projects.<id>.disabledSkills / disabledMcp): those names aren't given to its
 * sessions, whatever agent runs them; the rest is as usual. One agent then works the way each project needs, instead
 * of one role per project offered in every project. Applied by the backend before each session's next turn
 * (ext.Manager.ForProject).
 */
export function ProjectExtDialog() {
  const d = useStore((s) => s.dialog);
  const ask = d?.kind === "projectExt" ? d : undefined;
  const [skillsOff, setSkillsOff] = useState<string[]>([]);
  const [mcpOff, setMcpOff] = useState<string[]>([]);
  const [view, setView] = useState<ExtView | null>(null);
  const version = useStore((s) => s.extVersion);
  useEffect(() => {
    if (!ask) return;
    const cur = get().config.projects?.[ask.project];
    setSkillsOff(cur?.disabledSkills ?? []);
    setMcpOff(cur?.disabledMcp ?? []);
  }, [ask?.project]);
  useEffect(() => {
    if (!ask) return;
    api
      .ext()
      .then(setView)
      .catch((e) => toast.error(t("agentSettings.extLoadFailed", { error: e.message })));
  }, [!!ask, version]);
  const save = () => {
    if (!ask) return;
    const { [ask.project]: _, ...rest } = get().config.projects ?? {};
    const conf: ProjectConf = {};
    if (skillsOff.length) conf.disabledSkills = skillsOff;
    if (mcpOff.length) conf.disabledMcp = mcpOff;
    setConfig({ projects: skillsOff.length || mcpOff.length ? { ...rest, [ask.project]: conf } : rest });
    set({ dialog: null });
  };
  const c = view ? candidates(view, "project") : null;
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && set({ dialog: null })}>
      {/* Taller than a phone's screen with both lists: scroll inside instead of losing the title and buttons */}
      <DialogContent className="max-h-[calc(100%-2rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("projectExt.title", { name: ask?.name ?? "" })}</DialogTitle>
          <DialogDescription>{t("projectExt.desc")}</DialogDescription>
        </DialogHeader>
        {!c ? (
          <div className="text-[12px] text-muted-foreground">{t("agentSettings.extLoading")}</div>
        ) : (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <OffList label="Skill" items={c.skills} off={skillsOff} onChange={setSkillsOff} note={t("projectExt.noteSkill")} />
            <OffList label="MCP" items={c.mcp} off={mcpOff} onChange={setMcpOff} note={t("projectExt.noteMcp")} />
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => set({ dialog: null })}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save}>{t("common.save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Every known one, checked = given; unchecking switches it off. Names typed in (not listed: a claude.ai connector, a
 * repository's skill) are switched off too, shown under the list with a way to take them back. */
function OffList({ label, items, off, onChange, note }: { label: string; items: Cand[]; off: string[]; onChange: (v: string[]) => void; note: string }) {
  const [name, setName] = useState("");
  const extra = off.filter((n) => !items.some((i) => i.name === n));
  const add = () => {
    const n = name.trim();
    if (n && !off.includes(n)) onChange([...off, n]);
    setName("");
  };
  return (
    <div className="space-y-1.5 rounded-md border bg-card p-2.5">
      <div className="flex items-center gap-2">
        <span className="font-medium">{label}</span>
        <span className="flex-1" />
        {off.length > 0 && <span className="text-[11px] text-muted-foreground">{t("projectExt.offCount", { n: off.length })}</span>}
      </div>
      <div className="max-h-52 space-y-0.5 overflow-auto">
        {items.length === 0 && <div className="text-[12px] text-muted-foreground">{t("agentSettings.nothingToPick")}</div>}
        {items.map((i) => (
          <label key={i.name} className="flex cursor-pointer items-start gap-2 rounded-sm px-1 py-0.5 hover:bg-accent" title={i.description}>
            <Checkbox
              className="mt-0.5"
              checked={!off.includes(i.name)}
              onCheckedChange={(on) => onChange(on ? off.filter((n) => n !== i.name) : [...off, i.name])}
            />
            <span className="min-w-0 flex-1 truncate text-[12px]">{i.name}</span>
            <span className="shrink-0 text-[11px] text-muted-foreground">{i.from}</span>
          </label>
        ))}
      </div>
      {extra.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {extra.map((n) => (
            <span key={n} className="inline-flex items-center gap-1 rounded-sm border px-1.5 py-0.5 text-[11px] text-muted-foreground line-through">
              {n}
              <button title={t("projectExt.removeName")} className="hover:text-foreground" onClick={() => onChange(off.filter((x) => x !== n))}>
                <X className="size-3" />
              </button>
            </span>
          ))}
        </div>
      )}
      <Input
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            add();
          }
        }}
        placeholder={t("projectExt.addPlaceholder")}
        className="h-7 text-[12px]"
      />
      <div className="text-[11px] leading-snug text-muted-foreground">{note}</div>
    </div>
  );
}
