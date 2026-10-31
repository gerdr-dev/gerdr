// 面板本体：顶上是界面现在报的错（和没合上的新版界面），下面两页 —— 和 agent 对话（在界面目录里开会话）、回到之前的一版。
import { AlertTriangle, ArrowUpCircle, CheckCircle2, ChevronDown, History, LifeBuoy, MessageSquare, RefreshCw, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { type Commit, type Hello, type UIStatus, ago, dismissErrors, errorText, pageErrors, req, upgradeText } from "./api";
import { Chat } from "./Chat";

export function Panel({
  open,
  width,
  onOpen,
  onWidth,
}: {
  open: boolean;
  width: number;
  onOpen: (v: boolean) => void;
  onWidth: (w: number, done?: boolean) => void;
}) {
  const [broken, recheck] = usePageBroken(!open);
  const [dark, setDark] = useState(false);
  const [hello, setHello] = useState<Hello | null>(null);
  const [helloErr, setHelloErr] = useState("");
  const [status, setStatus] = useState<UIStatus | null>(null);
  const [statusErr, setStatusErr] = useState("");
  const [tab, setTab] = useState<"chat" | "history">("chat");
  const [histTick, setHistTick] = useState(0);

  const loadStatus = useCallback(() => {
    req<UIStatus>("GET", "/api/ui/status")
      .then((s) => (setStatus(s), setStatusErr("")))
      .catch((e) => setStatusErr(e.message));
  }, []);
  const refresh = useCallback(() => {
    loadStatus();
    setHistTick((t) => t + 1);
  }, [loadStatus]);

  // 第一次打开才去拉数据：没打开的时候这个脚本几乎不干活，不拖慢主界面
  useEffect(() => {
    if (!open || hello) return;
    req<Hello>("GET", "/api/hello")
      .then(setHello)
      .catch((e) => setHelloErr(e.message));
    loadStatus();
  }, [open, hello, loadStatus]);

  // 颜色跟 gerdr 的设置走（主界面可能已经坏了，不能靠它往 html 上加的 dark class）
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const t = hello?.config?.theme || "system";
      setDark(t === "dark" || (t === "system" && mq.matches));
    };
    apply();
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, [hello]);

  if (!open)
    return broken ? (
      <div className={"root" + (dark ? " dark" : "")}>
        <div className="pill">
          <button className="open" onClick={() => onOpen(true)} title="界面报错了：打开急救面板，叫 agent 来修">
            <LifeBuoy size={15} />
            急救
          </button>
          {/* 临时的错、不想管的错：点掉，别一直挡着下面的按钮（手机上正好压在发送键上） */}
          <button className="x" onClick={() => dismissErrors().then(recheck)} title="忽略这些错误">
            <X size={14} />
          </button>
        </div>
      </div>
    ) : null;

  const err = errorText(status);
  const upgrade = upgradeText(status);
  return (
    <div className={"root" + (dark ? " dark" : "")}>
      <aside className="panel" style={{ width }}>
        <Resizer width={width} onWidth={onWidth} />
        <header className="head">
          <div className="logo">
            <LifeBuoy size={16} />
          </div>
          <div className="title">
            <b>急救</b>
            <span>内置在 gerdr 里，主界面怎么改坏它都在</span>
          </div>
          <button className="icon" onClick={refresh} title="重新读一遍错误和历史">
            <RefreshCw size={15} />
          </button>
          <button className="icon" onClick={() => onOpen(false)} title="关闭急救面板">
            <X size={16} />
          </button>
        </header>

        {/* Everything below the header scrolls when the screen is too short (landscape phone, keyboard up) */}
        <div className="body">
          {/* A skipped upgrade alone isn't "broken": say that instead of the green "no errors" */}
          {!(upgrade && !err) && <StatusCard status={status} error={statusErr} text={err} onDismiss={() => dismissErrors().then(loadStatus)} />}
          {upgrade && (
            <div className="status warn">
              <div className="row">
                <ArrowUpCircle size={15} />
                <span className="grow">有新版界面没合上</span>
              </div>
              <p>{upgrade}</p>
              <small>开个新对话，让 agent 把新版合进来、两边的改动都留着。</small>
            </div>
          )}

          <nav className="tabs">
            <button className={tab === "chat" ? "on" : ""} onClick={() => setTab("chat")}>
              <MessageSquare size={14} />叫 agent 修
            </button>
            <button className={tab === "history" ? "on" : ""} onClick={() => setTab("history")}>
              <History size={14} />
              回到之前的一版
            </button>
          </nav>

          {helloErr ? (
            <div className="empty">连不上 gerdr：{helloErr}</div>
          ) : !hello || !status ? (
            <div className="empty">读取中…</div>
          ) : (
            <>
              {/* 两页都一直挂着（只是藏起来）：切过去再切回来，输入框里没发的字和滚动位置都还在 */}
              <div className="page" hidden={tab !== "chat"}>
                <Chat hello={hello} uiDir={status.dir} errors={err} upgrade={upgrade} onTurnDone={refresh} />
              </div>
              <div className="page" hidden={tab !== "history"}>
                <Versions tick={histTick} onRestored={() => setTimeout(loadStatus, 800)} />
              </div>
            </>
          )}
        </div>
      </aside>
    </div>
  );
}

// 页面有没有报错（关着的时候用：报了就在右下角冒一个「急救」）。错误是页面外壳一开始就挂好的收集器记的，主界面崩了也记得到。
// 编译出错不看 __GERDR_UI__.error（页面加载时的那份，修好之前一直在）：编译错误收集器里也有一条，换了新版就被丢掉。
function usePageBroken(active: boolean) {
  const [broken, setBroken] = useState(false);
  const check = useCallback(() => setBroken(pageErrors().length > 0), []);
  useEffect(() => {
    if (!active) return;
    check();
    const t = setInterval(check, 2000);
    return () => clearInterval(t);
  }, [active, check]);
  return [broken, check] as const;
}

// 左边缘拖一拖改宽度
function Resizer({ width, onWidth }: { width: number; onWidth: (w: number, done?: boolean) => void }) {
  return (
    <div
      className="resizer"
      onPointerDown={(e) => {
        e.preventDefault();
        const x0 = e.clientX;
        const w0 = width;
        const el = e.currentTarget;
        el.setPointerCapture(e.pointerId);
        let last = w0;
        const move = (ev: PointerEvent) => onWidth((last = w0 + x0 - ev.clientX));
        const up = () => {
          el.removeEventListener("pointermove", move);
          el.removeEventListener("pointerup", up);
          onWidth(last, true);
        };
        el.addEventListener("pointermove", move);
        el.addEventListener("pointerup", up);
      }}
    />
  );
}

function StatusCard({ status, error, text, onDismiss }: { status: UIStatus | null; error: string; text: string; onDismiss: () => void }) {
  const [more, setMore] = useState(false);
  if (error) return <div className="status bad">读不到界面状态：{error}</div>;
  if (!status) return null;
  const dirty = status.git?.uncommitted_files;
  if (!text)
    return (
      <div className="status ok">
        <CheckCircle2 size={15} />
        <div>
          界面现在没报错
          <small>运行时错误要主界面开着才报得上来{dirty ? ` · ${dirty} 个文件改了还没提交` : ""}</small>
        </div>
      </div>
    );
  return (
    <div className="status bad">
      <div className="row">
        <AlertTriangle size={15} />
        <span className="grow">界面报错了</span>
        {/* 编译错误点了也没用（改好之前一直在），只给运行时错误 */}
        {!status.build_error && (
          <button className="link" onClick={onDismiss} title="临时的错、已经修好了的错：清掉">
            忽略
          </button>
        )}
        <button className="link" onClick={() => setMore(!more)}>
          {more ? "收起" : "展开"}
          <ChevronDown size={13} style={{ transform: more ? "rotate(180deg)" : undefined }} />
        </button>
      </div>
      <pre className={more ? "" : "clip"}>{text}</pre>
    </div>
  );
}

function Versions({ tick, onRestored }: { tick: number; onRestored: () => void }) {
  const [list, setList] = useState<Commit[] | null>(null);
  const [err, setErr] = useState("");
  const [armed, setArmed] = useState("");
  const [busy, setBusy] = useState("");
  const [flash, setFlash] = useState<{ text: string; bad?: boolean } | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  const load = useCallback(() => {
    req<Commit[]>("GET", "/api/ui/history")
      .then((l) => (setList(l), setErr("")))
      .catch((e) => setErr(e.message));
  }, []);
  useEffect(load, [load, tick]);

  const restore = async (c: Commit) => {
    // 点两下才动手：防手滑（不用浏览器的 confirm，Electron 里会卡住页面）
    if (armed !== c.sha) {
      setArmed(c.sha);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setArmed(""), 4000);
      return;
    }
    setArmed("");
    setBusy(c.sha);
    try {
      const r = await req<{ saved?: string }>("POST", "/api/ui/restore", { sha: c.sha });
      setFlash({ text: `已回到 ${c.sha.slice(0, 8)}${r.saved ? `，之前的样子存成了 ${r.saved.slice(0, 8)}` : ""}。左边的主界面会跟着变，没变就刷新一下。` });
      onRestored();
    } catch (e: any) {
      setFlash({ text: "回退失败：" + e.message, bad: true });
    } finally {
      setBusy("");
      load();
    }
  };

  return (
    <div className="versions">
      <p className="hint">先让主界面能用上，再慢慢修。回退前现在的样子会先存一版，改动不会丢，还能再回来。</p>
      {flash && <div className={"flash" + (flash.bad ? " bad" : "")}>{flash.text}</div>}
      {err && <div className="flash bad">{err}</div>}
      {list && !list.length && <div className="empty">没有历史</div>}
      <ol>
        {list?.map((c) => (
          <li key={c.sha} className={c.current ? "cur" : ""}>
            <i />
            <div className="grow">
              <div className="subj" title={c.subject}>
                {c.subject || c.sha.slice(0, 8)}
              </div>
              <div className="meta">
                {c.current && <span className="tag brand">现在</span>}
                {c.upstream && <span className="tag">gerdr 原版</span>}
                <code>{c.sha.slice(0, 8)}</code> · {ago(c.time)}
              </div>
            </div>
            {!c.current && (
              <button className={"btn sm" + (armed === c.sha ? " danger" : "")} disabled={!!busy} onClick={() => restore(c)}>
                {busy === c.sha ? "回退中…" : armed === c.sha ? "确定？再点一次" : "回到这一版"}
              </button>
            )}
          </li>
        ))}
      </ol>
    </div>
  );
}
