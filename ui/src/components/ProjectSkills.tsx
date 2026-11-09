import { Pencil, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { SkillEditor } from "./ExtSettings";
import { api } from "@/lib/api";
import { askConfirm } from "@/lib/confirm";
import { t } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import type { ExtSkill } from "@/lib/types";

/**
 * A Gerdr project's own skills (<GERDR_DIR>/projects/<id>/skills, ext.ProjectSkillsDir): kept on this computer and given
 * only to the project's sessions, whatever agent runs them, before Gerdr's skills of the same name. A session that is
 * running picks an added or removed one up before its next turn; an edit is live.
 */
export function ProjectSkills({ project, open }: { project: string; open: boolean }) {
  const [skills, setSkills] = useState<ExtSkill[] | null>(null);
  const [edit, setEdit] = useState<{ name: string; content: string; isNew: boolean } | null>(null);
  // Bumped by the backend when any skill folder changes (an agent or `gerdr skill` adding one): refetch
  const version = useStore((s) => s.extVersion);
  const load = () =>
    api
      .projectSkills(project)
      .then((r) => setSkills(r.skills))
      .catch((e) => toast.error(t("projectSkills.loadFailed", { error: e.message })));
  useEffect(() => {
    if (open) load();
  }, [open, project, version]);

  const openEdit = async (name: string) => {
    try {
      const r = await api.readProjectSkill(project, name);
      setEdit({ name, content: r.content, isNew: false });
    } catch (e: any) {
      toast.error(e.message);
    }
  };
  const remove = async (sk: ExtSkill) => {
    const ok = await askConfirm({
      title: t("projectSkills.deleteTitle", { name: sk.name }),
      description: t("projectSkills.deleteDescription"),
      action: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      setSkills((await api.deleteProjectSkill(project, dirName(sk))).skills);
    } catch (e: any) {
      toast.error(e.message);
    }
  };

  if (edit)
    return (
      <SkillEditor
        edit={edit}
        onCancel={() => setEdit(null)}
        onSave={async (name, content) => {
          if (edit.isNew && skills?.some((s) => dirName(s) === name)) {
            toast.error(t("extSettings.skillExists", { name }));
            return;
          }
          try {
            setSkills((await api.writeProjectSkill(project, name, content)).skills);
            setEdit(null);
          } catch (e: any) {
            toast.error(e.message);
          }
        }}
      />
    );

  return (
    <div className="min-w-0 space-y-2 text-[13px]">
      <div className="flex items-start gap-2">
        <div className="flex-1 text-[12px] text-muted-foreground">{t("projectSkills.intro")}</div>
        <Button size="sm" variant="outline" className="shrink-0 text-[12px]" onClick={() => setEdit({ name: "", content: "", isNew: true })}>
          <Plus className="size-3.5" /> {t("extSettings.new")}
        </Button>
      </div>
      {!skills ? (
        <div className="py-4 text-[12px] text-muted-foreground">{t("common.loading")}</div>
      ) : skills.length === 0 ? (
        <div className="rounded-md border border-dashed px-3 py-6 text-center text-[12px] text-muted-foreground">{t("projectSkills.empty")}</div>
      ) : (
        <div className="divide-y rounded-md border">
          {skills.map((sk) => (
            <div key={sk.dir} className="group/sk flex items-center gap-2.5 px-3 py-2">
              <button className="min-w-0 flex-1 text-left" onClick={() => openEdit(dirName(sk))}>
                <div className="truncate">{sk.name}</div>
                {sk.description && <div className="truncate text-[11px] text-muted-foreground">{sk.description}</div>}
              </button>
              <Button size="icon" variant="ghost" className="size-7" title={t("common.edit")} onClick={() => openEdit(dirName(sk))}>
                <Pencil className="size-3.5" />
              </Button>
              <Button size="icon" variant="ghost" className="size-7 hover:text-bad" title={t("common.delete")} onClick={() => remove(sk)}>
                <Trash2 className="size-3.5" />
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** The folder's name is the skill's id in the API (SKILL.md's name may differ). */
const dirName = (sk: ExtSkill) => sk.dir.split(/[/\\]/).pop()!;
