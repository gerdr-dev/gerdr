import { toast } from "sonner";
import { api } from "@/lib/api";
import { askConfirm } from "@/lib/confirm";
import { t } from "@/lib/i18n";
import { panelsChanged, type Panel } from "@/lib/panels";

/**
 * Remove a panel (the rail's menu, Settings → Panels), the way deleting a project goes: confirm, move it to the trash,
 * and a toast with an undo that puts it back as it was (POST /api/work/panels/unremove).
 */
export async function removePanel(p: Pick<Panel, "key" | "title">) {
  const ok = await askConfirm({
    title: t("workSource.removeTitle", { name: p.title }),
    description: t("workSource.removeBody"),
    action: t("workSource.remove"),
    destructive: true,
  });
  if (!ok) return;
  try {
    const { trash } = await api.removePanel(p.key);
    panelsChanged();
    toast.success(t("workSource.removed", { name: p.title }), {
      description: t("workSource.oldIn", { path: trash }),
      duration: 10000,
      action: {
        label: t("spaceSwitcher.undo"),
        onClick: () =>
          api.unremovePanel(p.key, trash).then(
            () => {
              panelsChanged();
              toast.success(t("workSource.unremoved", { name: p.title }));
            },
            (e) => toast.error(t("spaceSwitcher.restoreFailed", { error: e?.message ?? e })),
          ),
      },
    });
  } catch (e: any) {
    toast.error(e?.message ?? String(e));
  }
}
