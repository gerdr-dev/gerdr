// 终端 pane 的内容：xterm.js 连服务端的 PTY（/api/terms/{id}/ws，协议见 internal/server/termapi.go）。
// shell 跑在 gerdr 那边，这里断了（刷新、睡眠、换网络）只是重连，连上时服务端把最近一段输出重放一遍。
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal, type ITheme } from "@xterm/xterm";
import { Loader2, RotateCcw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { closePaneById, reopenTerminal } from "@/lib/actions";
import { api, ApiError } from "@/lib/api";
import { eventKey } from "@/lib/keys";
import { isCoarse } from "@/lib/phone";
import { get } from "@/lib/store";
import { cn } from "@/lib/utils";

type State = "connecting" | "live" | "exited" | "gone";

// 同 index.css 里的 --font-mono（@theme inline 的变量不落到 :root 上，读不到）
const FONT = '"SF Mono", "JetBrains Mono", Menlo, Monaco, Consolas, "Liberation Mono", monospace';

export function TerminalView({
  paneId,
  termId,
  cwd,
  focused,
  onTitle,
}: {
  paneId: string;
  termId: string;
  cwd: string;
  focused: boolean;
  onTitle?: (t: string) => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const xt = useRef<Terminal | null>(null);
  const sendRef = useRef<(d: string) => void>(() => {});
  const ctrl = useRef(false);
  const [ctrlOn, setCtrlOn] = useState(false);
  const [state, setState] = useState<State>("connecting");
  const [code, setCode] = useState(0);
  const titleRef = useRef(onTitle);
  titleRef.current = onTitle;

  useEffect(() => {
    const el = box.current!;
    const term = new Terminal({
      fontFamily: FONT,
      fontSize: 12.5,
      lineHeight: 1.15,
      cursorBlink: true,
      scrollback: 10000,
      macOptionIsMeta: true,
      allowProposedApi: true,
      theme: readTheme(),
    });
    xt.current = term;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon((_e, uri) => window.open(uri, "_blank", "noopener")));
    term.open(el);

    let ws: WebSocket | null = null;
    let disposed = false;
    let exited = false;
    let retry = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let pingTimer: ReturnType<typeof setInterval> | undefined;
    let lastPong = 0;
    const enc = new TextEncoder();

    const sendBytes = (b: Uint8Array) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(b);
    };
    const send = (d: string) => sendBytes(enc.encode(d));
    sendRef.current = send;
    const sendSize = () => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "resize", cols: term.cols, rows: term.rows }));
    };
    // 藏着的 tab 里宽高是 0：这时候 fit 会算出 1 列，shell 按 1 列重排，切回来全乱了
    const doFit = () => {
      if (!el.offsetWidth || !el.offsetHeight) return;
      try {
        fit.fit();
      } catch {}
    };

    term.onData((d) => {
      // 手机键盘上的 Ctrl（按一下亮着，作用于下一个字母）
      if (ctrl.current && d.length === 1 && /[a-z@\[\\\]^_ ]/i.test(d)) {
        ctrl.current = false;
        setCtrlOn(false);
        const c = d.toUpperCase().charCodeAt(0);
        return send(String.fromCharCode(d === " " ? 0 : c & 0x1f));
      }
      send(d);
    });
    term.onBinary((d) => sendBytes(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff)));
    term.onResize(sendSize);
    term.onTitleChange((t) => titleRef.current?.(t));

    const schedule = () => {
      if (disposed || exited) return;
      setState("connecting");
      timer = setTimeout(connect, Math.min(5000, 300 * 2 ** retry++));
    };
    const connect = () => {
      if (disposed) return;
      const proto = location.protocol === "https:" ? "wss:" : "ws:";
      const s = new WebSocket(`${proto}//${location.host}/api/terms/${encodeURIComponent(termId)}/ws`);
      s.binaryType = "arraybuffer";
      ws = s;
      let opened = false;
      s.onopen = () => {
        opened = true;
        retry = 0;
        lastPong = Date.now();
        // 接下来第一帧是服务端重放的那一段：清屏后整段重画，不然重连一次屏幕上就多一份
        term.reset();
        setState("live");
        doFit();
        sendSize();
      };
      s.onmessage = (ev) => {
        if (typeof ev.data !== "string") {
          term.write(new Uint8Array(ev.data));
          return;
        }
        let m: any;
        try {
          m = JSON.parse(ev.data);
        } catch {
          return;
        }
        if (m.t === "pong") lastPong = Date.now();
        if (m.t === "exit") {
          exited = true;
          setCode(m.code);
          setState("exited");
          // 和 tmux 一样：shell 自己退的（exit、ctrl+d）就把 pane 收掉 —— 不看退出码：ctrl+d 时 zsh 带的是上一条命令的
          // 退出码，前面 ctrl+c 过就是 130。只有被信号杀掉的（-1：gerdr 退出、被 kill）留着，给「重开」。
          if (m.code >= 0) {
            api.termKill(termId).catch(() => {});
            closePaneById(paneId);
          }
        }
      };
      s.onclose = () => {
        if (ws !== s || disposed || exited) return;
        if (opened) return schedule();
        // 连不上：先问一句是终端没了（gerdr 重启过）还是网络问题
        api
          .termInfo(termId)
          .then(schedule)
          .catch((e) => (e instanceof ApiError && e.status === 404 ? setState("gone") : schedule()));
      };
    };
    connect();

    // 应用层心跳：休眠唤醒 / 换网络之后 WebSocket 常常「假在线」，浏览器自己要很久才发现
    pingTimer = setInterval(() => {
      if (ws?.readyState !== WebSocket.OPEN) return;
      if (Date.now() - lastPong > 45000) return ws.close();
      ws.send(JSON.stringify({ t: "ping" }));
    }, 15000);

    const ro = new ResizeObserver(() => doFit());
    ro.observe(el);
    // 主题跟着 html 上的 dark class 换
    const mo = new MutationObserver(() => (term.options.theme = readTheme()));
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });

    return () => {
      disposed = true;
      clearTimeout(timer);
      clearInterval(pingTimer);
      ro.disconnect();
      mo.disconnect();
      ws?.close();
      term.dispose();
      xt.current = null;
    };
  }, [termId, paneId]);

  // 焦点切到这个 pane（或者前缀动作做完）时把光标放进终端。触屏上不自动聚焦（会弹键盘）。
  useEffect(() => {
    if (!focused) return;
    const f = () => {
      if (!get().dialog && !document.querySelector("[role=dialog]") && !isCoarse()) xt.current?.focus();
    };
    f();
    // 按两下前缀：把前缀键本身（比如 ^B）发给 shell
    const lit = (e: Event) => {
      const k = (e as CustomEvent<KeyboardEvent>).detail;
      const { key } = eventKey(k);
      if (k.ctrlKey && /^[a-z]$/.test(key)) sendRef.current(String.fromCharCode(key.charCodeAt(0) - 96));
    };
    window.addEventListener("gerdr:refocus", f);
    window.addEventListener("gerdr:prefix-literal", lit);
    return () => {
      window.removeEventListener("gerdr:refocus", f);
      window.removeEventListener("gerdr:prefix-literal", lit);
    };
  }, [focused]);

  const key = (d: string) => {
    sendRef.current(d);
    xt.current?.focus();
  };

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      {/* 左边留一点：字贴着边框不好看。背景用终端自己的（xterm 画的就是 --background） */}
      <div ref={box} className="min-h-0 flex-1 overflow-hidden pl-2 pt-1" onMouseDown={() => xt.current?.focus()} />
      {isCoarse() && state === "live" && (
        // 手机键盘上没有的那几个键
        <div className="flex shrink-0 gap-1 overflow-x-auto border-t px-1.5 py-1" onPointerDown={(e) => e.preventDefault()}>
          {(
            [
              ["Esc", "\x1b"],
              ["Tab", "\t"],
              ["Ctrl", ""],
              ["^C", "\x03"],
              ["↑", "\x1b[A"],
              ["↓", "\x1b[B"],
              ["←", "\x1b[D"],
              ["→", "\x1b[C"],
              ["|", "|"],
              ["~", "~"],
              ["/", "/"],
              ["-", "-"],
            ] as const
          ).map(([label, seq]) => (
            <button
              key={label}
              className={cn("shrink-0 rounded-sm border px-2.5 py-1 font-mono text-[12px]", label === "Ctrl" && ctrlOn && "border-brand bg-brand/15 text-brand")}
              onClick={() => {
                if (label === "Ctrl") {
                  ctrl.current = !ctrl.current;
                  setCtrlOn(ctrl.current);
                  xt.current?.focus();
                } else key(seq);
              }}
            >
              {label}
            </button>
          ))}
        </div>
      )}
      {state === "connecting" && (
        <div className="pointer-events-none absolute right-2 top-2 flex items-center gap-1.5 rounded-sm bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
          <Loader2 className="size-3 animate-spin" />
          连接中
        </div>
      )}
      {(state === "gone" || state === "exited") && (
        <div className="absolute inset-x-0 bottom-0 flex items-center gap-3 border-t bg-background/95 px-3 py-2 text-[12px]">
          <span className="text-muted-foreground">{state === "gone" ? "这个终端已经不在了（gerdr 重启过）" : code < 0 ? "shell 被结束了" : `shell 已退出（${code}）`}</span>
          <Button size="sm" variant="outline" className="ml-auto h-7" onClick={() => reopenTerminal(paneId, cwd)}>
            <RotateCcw className="size-3.5" />在 {cwd.replace(/\/+$/, "").split("/").pop() || cwd} 重开
          </Button>
        </div>
      )}
    </div>
  );
}

// ---- 主题：从 index.css 的变量取（oklch 之类 xterm 认不了，画到 canvas 上读回 rgb） ----

let probe: CanvasRenderingContext2D | null = null;

function toRGB(css: string, alpha = 1): string {
  probe ??= document.createElement("canvas").getContext("2d", { willReadFrequently: true });
  if (!probe || !css) return css;
  probe.clearRect(0, 0, 1, 1);
  probe.fillStyle = css;
  probe.fillRect(0, 0, 1, 1);
  const [r, g, b] = probe.getImageData(0, 0, 1, 1).data;
  return alpha < 1 ? `rgba(${r}, ${g}, ${b}, ${alpha})` : `rgb(${r}, ${g}, ${b})`;
}

const ANSI = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"] as const;

function readTheme(): ITheme {
  const cs = getComputedStyle(document.documentElement);
  const v = (n: string) => cs.getPropertyValue(n).trim();
  const t: Record<string, string> = {
    background: toRGB(v("--background")),
    foreground: toRGB(v("--foreground")),
    cursor: toRGB(v("--foreground")),
    cursorAccent: toRGB(v("--background")),
    selectionBackground: toRGB(v("--brand"), 0.3),
    scrollbarSliderBackground: toRGB(v("--muted-foreground"), 0.25),
    scrollbarSliderHoverBackground: toRGB(v("--muted-foreground"), 0.4),
    scrollbarSliderActiveBackground: toRGB(v("--muted-foreground"), 0.5),
  };
  ANSI.forEach((name, i) => {
    t[name] = toRGB(v(`--term-${i}`));
    t["bright" + name[0].toUpperCase() + name.slice(1)] = toRGB(v(`--term-${i + 8}`));
  });
  return t as ITheme;
}
