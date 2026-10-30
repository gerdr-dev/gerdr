// OKR for the Work view: my objectives / key results this quarter, when I last wrote a progress
// report and whether this week's is in. Everything comes from the "okr" MCP server (whatever
// gerdr or Claude Code has configured under that name).
//
// Weekly reports are keyed by a week string like "2026W1019-1025": the Monday's year, then
// Monday–Sunday as MMDD.

const SERVER = "okr";

type Obj = {
  id: number;
  name: string;
  desc: string;
  quarter: string;
  label: "O" | "KR" | "Task";
  parent_id: number;
  progress_percent: number;
  confidence_level: string;
  linked_object?: { id: number; name: string; owner_name: string } | null;
};
type Report = { id: number; object_id: number; object_name: string; week: string; content: string; progress_percentage: number; create_time: string };

const pad = (n: number) => String(n).padStart(2, "0");
const ymd = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const mmdd = (d: Date) => `${pad(d.getMonth() + 1)}${pad(d.getDate())}`;

export function weekOf(now: Date) {
  const mon = new Date(now.getFullYear(), now.getMonth(), now.getDate() - ((now.getDay() + 6) % 7));
  const sun = new Date(mon.getFullYear(), mon.getMonth(), mon.getDate() + 6);
  return { key: `${mon.getFullYear()}W${mmdd(mon)}-${mmdd(sun)}`, start: ymd(mon), end: ymd(sun) };
}

export async function summary(input: { quarter?: string } | undefined, ctx: any) {
  const me = await ctx.mcp(SERVER, "get_current_user", {});
  const { quarters } = await ctx.mcp(SERVER, "list_quarters", { type: "quarterly" });
  const quarter = quarters.find((q: any) => q.name === input?.quarter) || quarters.find((q: any) => q.is_active) || quarters[0];
  const [{ objects }, { reports }] = await Promise.all([
    ctx.mcp(SERVER, "list_okr_objects", { my_objects_only: true, quarter: quarter.name }),
    ctx.mcp(SERVER, "list_progress_reports", { owner_id: me.id, quarter: quarter.name }),
  ]);
  let all: Report[] = reports || [];
  // Nothing yet this quarter: look one quarter back so "last report" still means something.
  if (all.length === 0) {
    const i = quarters.indexOf(quarter);
    const prev = quarters[i + 1];
    if (prev) all = (await ctx.mcp(SERVER, "list_progress_reports", { owner_id: me.id, quarter: prev.name })).reports || [];
  }
  all.sort((a, b) => b.create_time.localeCompare(a.create_time));

  const week = weekOf(new Date());
  const latest = new Map<number, Report>();
  for (const r of all) if (!latest.has(r.object_id)) latest.set(r.object_id, r);
  const thisWeek = new Set(all.filter((r) => r.week === week.key).map((r) => r.object_id));

  const objs: Obj[] = objects || [];
  const kr = (o: Obj) => {
    const r = latest.get(o.id);
    return {
      id: o.id,
      name: o.name,
      desc: o.desc,
      progress: o.progress_percent,
      confidence: o.confidence_level,
      reportedThisWeek: thisWeek.has(o.id),
      lastReport: r ? { week: r.week, time: r.create_time, content: r.content, progress: r.progress_percentage } : null,
    };
  };
  const os = objs.filter((o) => o.label === "O");
  const mine = new Set(os.map((o) => o.id));
  const objectives = os.map((o) => ({
    id: o.id,
    name: o.name,
    desc: o.desc,
    progress: o.progress_percent,
    confidence: o.confidence_level,
    krs: objs.filter((k) => k.label === "KR" && k.parent_id === o.id).map(kr),
  }));
  // KRs I own under someone else's objective (aligned upward): one group per that objective.
  const orphans = objs.filter((k) => k.label === "KR" && !mine.has(k.parent_id));
  const groups = new Map<string, any>();
  for (const k of orphans) {
    const title = k.linked_object ? `${k.linked_object.name}（${k.linked_object.owner_name}）` : "Other key results";
    if (!groups.has(title)) groups.set(title, { id: -(k.linked_object?.id || 0) || -k.id, name: title, desc: "", progress: null, confidence: "", aligned: true, krs: [] });
    groups.get(title).krs.push(kr(k));
  }

  const last = all[0];
  return {
    user: { id: me.id, name: me.first_name || me.name, title: me.title },
    quarter: { name: quarter.name, start: quarter.start_date, end: quarter.end_date },
    week,
    submittedThisWeek: thisWeek.size > 0,
    lastReport: last ? { time: last.create_time, week: last.week, object: last.object_name } : null,
    objectives: [...objectives, ...groups.values()],
  };
}

