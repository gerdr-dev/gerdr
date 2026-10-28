// agent 回复的极简 Markdown：代码块、标题、列表、行内代码、加粗。
// 不引 markdown 库：急救面板打进二进制，越小越好；修界面的对话用不上表格、公式那些。
import type { ReactNode } from "react";

export function Markdown({ text }: { text: string }) {
  const out: ReactNode[] = [];
  const parts = text.split(/^```[^\n]*\n?/m);
  // split 之后奇数段是代码块里面（没闭合的最后一段也当代码：流式输出到一半）
  parts.forEach((p, i) => {
    if (i % 2) out.push(<pre key={i} className="code">{p.replace(/\n$/, "")}</pre>);
    else blocks(p, i, out);
  });
  return <div className="md">{out}</div>;
}

function blocks(src: string, k: number, out: ReactNode[]) {
  let list = null as { ordered: boolean; items: string[] } | null;
  let para: string[] = [];
  let n = 0;
  const flushPara = () => {
    if (para.length) out.push(<p key={`${k}-${n++}`}>{inline(para.join("\n"))}</p>);
    para = [];
  };
  const flushList = () => {
    if (!list) return;
    const items = list.items.map((t, i) => <li key={i}>{inline(t)}</li>);
    out.push(list.ordered ? <ol key={`${k}-${n++}`}>{items}</ol> : <ul key={`${k}-${n++}`}>{items}</ul>);
    list = null;
  };
  for (const line of src.split("\n")) {
    const h = /^(#{1,6})\s+(.*)/.exec(line);
    const li = /^\s*([-*]|\d+[.)])\s+(.*)/.exec(line);
    if (h) {
      flushPara(), flushList();
      out.push(<h4 key={`${k}-${n++}`}>{inline(h[2])}</h4>);
    } else if (li) {
      flushPara();
      const ordered = /\d/.test(li[1]);
      if (list && list.ordered !== ordered) flushList();
      (list ??= { ordered, items: [] }).items.push(li[2]);
    } else if (!line.trim()) {
      flushPara(), flushList();
    } else if (list && /^\s+/.test(line)) {
      list.items[list.items.length - 1] += " " + line.trim();
    } else {
      flushList();
      para.push(line);
    }
  }
  flushPara(), flushList();
}

function inline(s: string): ReactNode[] {
  return s.split(/(`[^`\n]+`|\*\*[^*\n]+\*\*)/).map((t, i) => {
    if (i % 2 === 0) return t;
    return t[0] === "`" ? <code key={i}>{t.slice(1, -1)}</code> : <b key={i}>{t.slice(2, -2)}</b>;
  });
}
