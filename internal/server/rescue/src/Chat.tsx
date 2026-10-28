// 和 agent 对话：只看 cwd 是界面目录的会话。只做修界面要的那点 —— 发消息、看回复和工具调用、审批 / 提问、打断；
// 别往里加主界面那些功能，这里越简单越不容易坏。
import { ArrowUp, Check, Loader2, Plus, Square, X } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { type Ev, type Hello, type Session, req, uuid } from "./api";
import { Markdown } from "./Markdown";

const STATUS: Record<string, string> = { idle: "空闲", running: "在干活…", waiting: "等你处理", error: "出错了", starting: "启动中…" };

export function Chat({ hello, uiDir, errors, onTurnDone }: { hello: Hello; uiDir: string; errors: string; onTurnDone: () => void }) {
  const [sessions, setSessions] = useState<Record<string, Session>>(() => Object.fromEntries(hello.sessions.map((s) => [s.id, s])));
  const mine = useMemo(
    () =>
      Object.values(sessions)
        .filter((s) => s.cwd === uiDir)
        .sort((a, b) => b.updatedAt - a.updatedAt),
    [sessions, uiDir],
  );
  // 急救只接着最近的一个对话，不列历史
  const [current, setCurrent] = useState(() => mine[0]?.id || "");
  const [events, setEvents] = useState<Ev[]>([]);
  const [agent, setAgent] = useState(() => {
    const d = hello.config?.defaultAgent;
    return hello.agents.find((a) => a.id === d && a.available)?.id || hello.agents.find((a) => a.available)?.id || "";
  });
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [creating, setCreating] = useState(false);
  const [alert, setAlert] = useState("");
  const ws = useWS(current, setSessions, setEvents, onTurnDone);
  const cur = sessions[current];
  const busy = cur && (cur.status === "running" || cur.status === "waiting");
  const ta = useRef<HTMLTextAreaElement>(null);

  const select = (id: string) => {
    setCurrent(id);
    setEvents([]);
    setAlert("");
  };

  const newSession = async () => {
    if (!agent) return;
    setCreating(true);
    try {
      const mode = hello.config?.defaultModes?.[agent];
      const s = await req<Session>("POST", "/api/sessions", { agent, cwd: uiDir, mode: mode || undefined, title: "急救：修界面" });
      setSessions((m) => ({ ...m, [s.id]: s }));
      select(s.id);
      // 先填好第一句（带上错误），人看一眼再发
      setInput(
        "gerdr 的界面（就是这个目录）被改坏了，帮我修好。先读 AGENTS.md，再跑 `gerdr ui status` 看错误；" +
          "修完再跑一次确认没有错误了。只改坏掉的地方，别顺手重构。" +
          (errors ? "\n\n现在报的错：\n" + errors : ""),
      );
      setTimeout(() => ta.current?.focus());
    } catch (e: any) {
      setAlert("开会话失败：" + e.message);
    } finally {
      setCreating(false);
    }
  };

  const send = async () => {
    const text = input.trim();
    if (!text || !current || sending) return;
    setSending(true);
    try {
      await req("POST", `/api/sessions/${current}/send`, { content: [{ type: "text", text }], id: uuid() });
      setInput("");
    } catch (e: any) {
      setAlert("发送失败：" + e.message);
    } finally {
      setSending(false);
    }
  };

  const answer = (reqId: string, response: unknown) =>
    req("POST", `/api/sessions/${current}/answer`, { reqId, response }).catch((e) => setAlert(e.message));

  // 输入框跟着内容长高
  useLayoutEffect(() => {
    const t = ta.current;
    if (!t) return;
    t.style.height = "auto";
    t.style.height = Math.min(t.scrollHeight, 240) + "px";
  }, [input]);

  return (
    <div className="chat">
      <div className="bar">
        {/* 不列历史对话：急救就接着最近那一个，要从头来点「新对话」 */}
        <span className="grow bar-hint">{cur ? `${hello.agents.find((a) => a.id === cur.agent)?.name || cur.agent} · 接着上次的对话` : "还没开过急救对话"}</span>
        <select className="agent" value={agent} onChange={(e) => setAgent(e.target.value)} title="新对话用哪个 agent">
          {hello.agents.map((a) => (
            <option key={a.id} value={a.id} disabled={!a.available}>
              {a.name}
            </option>
          ))}
        </select>
        <button className="btn" onClick={newSession} disabled={creating || !agent} title="在界面目录里开一个新对话，错误信息自动带上">
          {creating ? <Loader2 size={14} className="spin" /> : <Plus size={14} />}
          新对话
        </button>
      </div>

      <Log events={events} current={current} answer={answer} alert={alert} connected={ws} />

      <div className="composer">
        {cur && (
          <div className="state">
            <span className={"dot " + cur.status} />
            {STATUS[cur.status] || cur.status}
            {cur.status === "error" && cur.error ? "：" + cur.error : ""}
          </div>
        )}
        <div className="box">
          <textarea
            ref={ta}
            rows={2}
            value={input}
            disabled={!current}
            placeholder={current ? "告诉 agent 哪里坏了（⌘/Ctrl + Enter 发送）" : "先开一个新对话"}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && !e.nativeEvent.isComposing) {
                e.preventDefault();
                send();
              }
            }}
          />
          {busy && !input.trim() ? (
            <button className="send stop" title="打断" onClick={() => req("POST", `/api/sessions/${current}/cancel`, {}).catch((e) => setAlert(e.message))}>
              <Square size={12} fill="currentColor" />
            </button>
          ) : (
            <button className="send" title="发送" disabled={!input.trim() || !current || sending} onClick={send}>
              {sending ? <Loader2 size={15} className="spin" /> : <ArrowUp size={16} />}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

// 推送：/ws，只订阅正在看的这一个会话。返回连没连上。
function useWS(
  current: string,
  setSessions: React.Dispatch<React.SetStateAction<Record<string, Session>>>,
  setEvents: React.Dispatch<React.SetStateAction<Ev[]>>,
  onTurnDone: () => void,
) {
  const [up, setUp] = useState(false);
  const sock = useRef<WebSocket | null>(null);
  const cur = useRef(current);
  cur.current = current;
  const done = useRef(onTurnDone);
  done.current = onTurnDone;

  const watch = () => {
    const s = sock.current;
    if (s?.readyState === WebSocket.OPEN) s.send(JSON.stringify({ t: "watch", ids: cur.current ? [cur.current] : [] }));
  };
  useEffect(watch, [current]);

  useEffect(() => {
    let stop = false;
    let retry = 0;
    let timer: ReturnType<typeof setTimeout>;
    const connect = () => {
      const s = new WebSocket(`${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/ws`);
      sock.current = s;
      s.onopen = () => {
        retry = 0;
        setUp(true);
        watch();
      };
      s.onmessage = (ev) => {
        let batch: any;
        try {
          batch = JSON.parse(ev.data);
        } catch {
          return;
        }
        if (!Array.isArray(batch)) batch = [batch];
        for (const m of batch) {
          if (m.t === "session" && m.session) {
            setSessions((all) => {
              const prev = all[m.id];
              // agent 一轮干完（多半刚改过界面）：上面的错误和版本列表跟着刷新
              if (m.id === cur.current && prev && (prev.status === "running" || prev.status === "waiting") && m.session.status !== prev.status && m.session.status !== "waiting")
                setTimeout(() => done.current(), 600);
              return { ...all, [m.id]: m.session };
            });
          } else if (m.t === "session_removed") {
            setSessions((all) => {
              const { [m.id]: _, ...rest } = all;
              return rest;
            });
          } else if (m.t === "events" && m.id === cur.current) {
            setEvents((list) => {
              const base = m.reset ? [] : list;
              const last = base.length ? base[base.length - 1].seq : 0;
              const add = (m.events || []).filter((e: Ev) => e.seq > last);
              return add.length || m.reset ? [...base, ...add] : list;
            });
          }
        }
      };
      s.onclose = () => {
        if (sock.current !== s) return;
        setUp(false);
        if (!stop) timer = setTimeout(connect, Math.min(8000, 300 * 2 ** retry++));
      };
    };
    connect();
    const ping = setInterval(() => sock.current?.readyState === WebSocket.OPEN && sock.current.send(JSON.stringify({ t: "ping" })), 20000);
    return () => {
      stop = true;
      clearTimeout(timer);
      clearInterval(ping);
      sock.current?.close();
      sock.current = null;
    };
  }, []);
  return up;
}

// ---- 日志 → 一列 ----

type Item =
  | { k: "user"; text: string }
  | { k: "text"; text: string }
  | { k: "tool"; id: string; title: string; status: string }
  | { k: "notice"; level?: string; text: string }
  | Req;
type Req = { k: "permission" | "ask"; reqId: string; request: any; done: boolean; response?: any };

function toItems(events: Ev[]): Item[] {
  // 流式片段拼成整段；工具调用按 id 合并；审批 / 提问画成卡片，答过的收起来
  const items: Item[] = [];
  const tools = new Map<string, Extract<Item, { k: "tool" }>>();
  const reqs = new Map<string, Req>();
  let text: Extract<Item, { k: "text" }> | null = null;
  const textOf = (c: Ev["content"]) => (c || []).map((x) => (x.type === "text" ? x.text : x.type === "resource_link" ? `[${x.name}]` : "")).join("");
  for (const e of events) {
    if (e.kind === "user") {
      text = null;
      items.push({ k: "user", text: textOf(e.content) });
    } else if (e.kind === "update" && e.update) {
      const u = e.update;
      if (u.sessionUpdate === "agent_message_chunk") {
        if (!text) items.push((text = { k: "text", text: "" }));
        text.text += u.content?.type === "text" ? u.content.text || "" : "";
      } else if ((u.sessionUpdate === "tool_call" || u.sessionUpdate === "tool_call_update") && u.toolCallId) {
        text = null;
        let t = tools.get(u.toolCallId);
        if (!t) {
          t = { k: "tool", id: u.toolCallId, title: "", status: "pending" };
          tools.set(u.toolCallId, t);
          items.push(t);
        }
        if (u.title) t.title = u.title;
        if (u.status) t.status = u.status;
      }
    } else if ((e.kind === "permission" || e.kind === "ask") && e.reqId) {
      text = null;
      const it = { k: e.kind, reqId: e.reqId, request: e.request, done: false } as Req;
      reqs.set(e.reqId, it);
      items.push(it);
    } else if ((e.kind === "permission_done" || e.kind === "ask_done") && e.reqId) {
      const it = reqs.get(e.reqId);
      if (it) (it.done = true), (it.response = e.response);
    } else if (e.kind === "turn_end") {
      text = null;
      if (e.error) items.push({ k: "notice", level: "error", text: e.error });
    } else if (e.kind === "notice") {
      text = null;
      items.push({ k: "notice", level: e.level, text: e.text || "" });
    }
  }
  return items;
}

function Log({
  events,
  current,
  answer,
  alert,
  connected,
}: {
  events: Ev[];
  current: string;
  answer: (reqId: string, r: unknown) => void;
  alert: string;
  connected: boolean;
}) {
  const items = useMemo(() => toItems(events), [events]);
  const box = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  // 贴着底部的时候跟着往下滚；人往上翻了就不打扰
  useLayoutEffect(() => {
    const b = box.current;
    if (b && stick.current) b.scrollTop = b.scrollHeight;
  }, [items, alert]);
  useEffect(() => {
    stick.current = true;
  }, [current]);

  return (
    <div className="log" ref={box} onScroll={(e) => (stick.current = e.currentTarget.scrollHeight - e.currentTarget.scrollTop - e.currentTarget.clientHeight < 80)}>
      {!current ? (
        <div className="empty big">
          <b>开一个新对话</b>
          agent 在界面目录里干活，上面的错误会自动带给它。修的时候左边的主界面跟着变。
        </div>
      ) : !items.length ? (
        <div className="empty">{connected ? "还没有消息" : "连接中…"}</div>
      ) : (
        items.map((it, i) => <Row key={i} it={it} answer={answer} />)
      )}
      {alert && <div className="notice error">{alert}</div>}
    </div>
  );
}

function Row({ it, answer }: { it: Item; answer: (reqId: string, r: unknown) => void }) {
  switch (it.k) {
    case "user":
      return <div className="user">{it.text}</div>;
    case "text":
      return <Markdown text={it.text} />;
    case "tool":
      return (
        <div className={"tool " + it.status} title={it.title}>
          {it.status === "completed" ? <Check size={13} /> : it.status === "failed" ? <X size={13} /> : <Loader2 size={13} className="spin" />}
          <span>{it.title || "工具调用"}</span>
        </div>
      );
    case "notice":
      return <div className={"notice" + (it.level === "error" ? " error" : "")}>{it.text}</div>;
    case "permission":
      return <PermissionCard it={it} answer={answer} />;
    case "ask":
      return <AskCard it={it} answer={answer} />;
  }
}

function PermissionCard({ it, answer }: { it: Req; answer: (reqId: string, r: unknown) => void }) {
  const [sent, setSent] = useState(false);
  const r = it.request || {};
  const title = r.toolCall?.title || "要你批准";
  const input = r.toolCall?.rawInput;
  const detail = input?.command || input?.file_path;
  if (it.done) {
    const opt = (r.options || []).find((o: any) => o.optionId === it.response?.optionId);
    return (
      <div className="card done">
        <span className="muted">批准</span> {title} <span className="muted">→ {opt ? opt.name : "已取消"}</span>
      </div>
    );
  }
  return (
    <div className="card">
      <div className="q">
        要你批准：<b>{title}</b>
      </div>
      {detail && <pre className="code">{detail}</pre>}
      <div className="opts">
        {(r.options || []).map((o: any) => (
          <button
            key={o.optionId}
            className={"btn sm" + (o.kind?.startsWith("allow") ? " primary" : "")}
            disabled={sent}
            onClick={() => (setSent(true), answer(it.reqId, { optionId: o.optionId }))}
          >
            {o.name}
          </button>
        ))}
      </div>
    </div>
  );
}

function AskCard({ it, answer }: { it: Req; answer: (reqId: string, r: unknown) => void }) {
  const qs: any[] = it.request?.questions || [];
  const [chosen, setChosen] = useState<Record<string, string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});
  const [sent, setSent] = useState(false);
  if (it.done) {
    const ans = it.response?.answers || {};
    return (
      <div className="card done">
        {qs.map((q) => (
          <div key={q.id}>
            {q.question} <span className="muted">→ {(ans[q.id] || []).join(", ") || "（没回答）"}</span>
          </div>
        ))}
      </div>
    );
  }
  const pick = (q: any, label: string) =>
    setChosen((c) => {
      const cur = c[q.id] || [];
      const has = cur.includes(label);
      return { ...c, [q.id]: q.multiSelect ? (has ? cur.filter((x) => x !== label) : [...cur, label]) : has ? [] : [label] };
    });
  const submit = () => {
    const answers: Record<string, string[]> = {};
    for (const q of qs) {
      const v = [...(chosen[q.id] || [])];
      const o = other[q.id]?.trim();
      answers[q.id] = o ? (q.multiSelect ? [...v, o] : [o]) : v;
    }
    setSent(true);
    answer(it.reqId, { answers });
  };
  return (
    <div className="card">
      {qs.map((q) => (
        <div key={q.id} className="qa">
          <div className="q">
            <b>{q.question}</b>
          </div>
          <div className="opts">
            {(q.options || []).map((o: any) => (
              <button key={o.label} title={o.description || ""} className={"btn sm" + ((chosen[q.id] || []).includes(o.label) ? " primary" : "")} onClick={() => pick(q, o.label)}>
                {o.label}
              </button>
            ))}
            <input placeholder="或者自己写" value={other[q.id] || ""} onChange={(e) => setOther((m) => ({ ...m, [q.id]: e.target.value }))} />
          </div>
        </div>
      ))}
      <div className="opts">
        <button className="btn sm primary" disabled={sent} onClick={submit}>
          提交
        </button>
        <button className="btn sm" disabled={sent} onClick={() => (setSent(true), answer(it.reqId, { cancelled: true, reason: "skipped" }))}>
          跳过
        </button>
      </div>
    </div>
  );
}
