import { cn } from "@/lib/utils";

/*
 * claude 的 /usage 输出画成卡片。
 *
 * 它是本地命令，输出原样当一段 agent 文字发过来（agent_message_chunk），是给终端看的排版：靠换行和两格缩进分层。
 * 交给 Markdown 画，换行全被吞掉，挤成一大段读不了。这里按行认出几种形状，认不出的行原样当小字留着 ——
 * claude 改了措辞也只是退回成「能读的纯文本」，不会丢内容。
 */

type Limit = { label: string; pct: number; reset?: string };
type Item = { pct: number; text: string; hint?: string };
type Section = { title: string; meta: string[]; items: Item[]; tops: { label: string; values: string[] }[]; extra: string[] };

const LIMIT = /^(Current [^:]+):\s*(\d+)% used(?:\s*·\s*resets (.+))?$/;
const ITEM = /^(\d+)%\s{2,}(.+)$/;
const TOP = /^(Top [^:]+):\s*(.+)$/;
const SECTION = /^(Last \S+)\s*·\s*(.+)$/;

function parse(text: string) {
  const head: string[] = [];
  const limits: Limit[] = [];
  const sections: Section[] = [];
  let note = "";
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    let m: RegExpExecArray | null;
    if ((m = LIMIT.exec(line))) {
      limits.push({ label: m[1], pct: Number(m[2]), reset: m[3] });
    } else if ((m = SECTION.exec(line))) {
      sections.push({ title: m[1], meta: m[2].split(/\s*·\s*/), items: [], tops: [], extra: [] });
    } else if (sections.length && (m = ITEM.exec(line))) {
      const [text, ...hint] = m[2].split(/\s*·\s*/);
      sections[sections.length - 1].items.push({ pct: Number(m[1]), text, hint: hint.join(" · ") || undefined });
    } else if (sections.length && (m = TOP.exec(line))) {
      sections[sections.length - 1].tops.push({ label: m[1], values: m[2].split(/,\s*/) });
    } else if (sections.length) {
      sections[sections.length - 1].extra.push(line);
    } else if (/^What's contributing/.test(line)) {
      // 小节标题，卡片里自己画
    } else if (/^Approximate/.test(line)) {
      note = line;
    } else {
      head.push(line);
    }
  }
  return { head, limits, sections, note };
}

// 「Current session」→「当前会话」，「Current week (all models)」→「本周 · 全部模型」，「Current week (Fable)」→「本周 · Fable」
function limitLabel(s: string) {
  const m = /^Current (session|week)(?:\s*\((.+)\))?$/.exec(s);
  if (!m) return s;
  const base = m[1] === "session" ? "当前会话" : "本周";
  const sub = m[2] === "all models" ? "全部模型" : m[2];
  return sub ? `${base} · ${sub}` : base;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

// 「Oct 28 at 11:19pm (Asia/Shanghai)」→「10/28 23:19 重置 · 还剩 13h」。认不出就原样（去掉时区）
function resetText(s: string, now = Date.now()) {
  const plain = s.replace(/\s*\([^)]*\)\s*$/, "");
  const m = /^([A-Z][a-z]{2}) (\d{1,2})(?: at (\d{1,2})(?::(\d{2}))?\s*(am|pm))?$/i.exec(plain);
  const mon = m ? MONTHS.indexOf(m[1].slice(0, 3)) : -1;
  if (!m || mon < 0) return `${plain} 重置`;
  let h = m[3] ? Number(m[3]) % 12 : 0;
  if (m[5]?.toLowerCase() === "pm") h += 12;
  const min = m[4] ? Number(m[4]) : 0;
  const d = new Date(new Date(now).getFullYear(), mon, Number(m[2]), h, min);
  // 跨年：12 月看到「Jan 2」
  if (d.getTime() < now - 86400e3 * 30) d.setFullYear(d.getFullYear() + 1);
  const left = d.getTime() - now;
  const when = `${mon + 1}/${m[2]} ${String(h).padStart(2, "0")}:${String(min).padStart(2, "0")}`;
  if (left <= 0) return `${when} 重置`;
  const hours = left / 3600e3;
  const rest =
    Math.round(left / 60e3) < 60 ? `${Math.max(1, Math.round(left / 60e3))}m` : hours < 48 ? `${Math.round(hours)}h` : `${Math.round(hours / 24)} 天`;
  return `${when} 重置 · 还剩 ${rest}`;
}

function tone(pct: number) {
  return pct >= 90 ? "bg-bad" : pct >= 70 ? "bg-warn" : "bg-brand";
}

// plain：「花在哪」那几条是占比、不是额度，不该随数大变黄变红
function Bar({ pct, plain, className }: { pct: number; plain?: boolean; className?: string }) {
  return (
    <div className={cn("h-1.5 overflow-hidden rounded-full bg-muted", className)}>
      <div className={cn("h-full rounded-full", plain ? "bg-brand/60" : tone(pct))} style={{ width: `${Math.min(100, Math.max(pct > 0 ? 2 : 0, pct))}%` }} />
    </div>
  );
}

// 「Last 24h」→「近 24 小时」，「Last 7d」→「近 7 天」
function sectionTitle(s: string) {
  const m = /^Last (\d+)(h|d)$/.exec(s);
  return m ? `近 ${m[1]} ${m[2] === "h" ? "小时" : "天"}` : s;
}

export function UsageCard({ text }: { text: string }) {
  const { head, limits, sections, note } = parse(text);
  return (
    <div className="my-1 overflow-hidden rounded-lg border bg-card text-[13px]">
      <div className="flex items-baseline justify-between gap-3 border-b px-3 py-2">
        <span className="font-medium">用量</span>
        {head.length > 0 && (
          <span className="min-w-0 truncate text-[11px] text-muted-foreground" title={head.join("\n")}>
            {/subscription/i.test(head[0]) ? "走的是订阅额度" : head.join(" ")}
          </span>
        )}
      </div>

      <div className="space-y-3 px-3 py-3">
        {limits.map((l) => (
          <div key={l.label}>
            <div className="mb-1.5 flex items-baseline gap-2">
              <span className="min-w-0 flex-1 truncate">{limitLabel(l.label)}</span>
              <span className="font-medium tabular-nums">{l.pct}%</span>
            </div>
            <Bar pct={l.pct} className="h-2" />
            {l.reset && <div className="mt-1 text-[11px] text-muted-foreground tabular-nums">{resetText(l.reset)}</div>}
          </div>
        ))}
      </div>

      {sections.length > 0 && (
        <div className="border-t px-3 py-3">
          <div className="mb-2 flex items-baseline justify-between gap-3">
            <span className="shrink-0 text-[12px] font-medium">用量花在哪</span>
            {note && (
              <span className="min-w-0 truncate text-[11px] text-muted-foreground" title={note}>
                {/^Approximate/.test(note) ? "估算 · 只算这台机器 · 各项有重叠" : note}
              </span>
            )}
          </div>
          <div className="space-y-4">
            {sections.map((s) => (
              <section key={s.title}>
                <div className="mb-1.5 flex flex-wrap items-baseline gap-x-2 text-[12px]">
                  <span className="font-medium">{sectionTitle(s.title)}</span>
                  <span className="text-[11px] text-muted-foreground tabular-nums">{s.meta.join(" · ")}</span>
                </div>
                <ul className="space-y-1.5">
                  {s.items.map((it, i) => (
                    <li key={i} className="grid grid-cols-[2.5rem_1fr] items-start gap-x-2">
                      <span className="text-right text-[12px] font-medium tabular-nums">{it.pct}%</span>
                      <div className="min-w-0">
                        <div className="leading-snug">{it.text}</div>
                        {it.hint && <div className="text-[11px] leading-snug text-muted-foreground">{it.hint}</div>}
                        <Bar pct={it.pct} plain className="mt-1 h-1" />
                      </div>
                    </li>
                  ))}
                </ul>
                {s.tops.length > 0 && (
                  <div className="mt-2 space-y-1">
                    {s.tops.map((t) => (
                      <div key={t.label} className="flex flex-wrap items-center gap-1 text-[11px]">
                        <span className="mr-0.5 text-muted-foreground">{t.label.replace(/^Top /, "")}</span>
                        {t.values.map((v) => (
                          <span key={v} className="rounded-sm bg-muted px-1.5 py-px font-mono tabular-nums">
                            {v}
                          </span>
                        ))}
                      </div>
                    ))}
                  </div>
                )}
                {s.extra.map((x, i) => (
                  <div key={i} className="mt-1 text-[11px] text-muted-foreground">
                    {x}
                  </div>
                ))}
              </section>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
