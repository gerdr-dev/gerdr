import { ArrowUp, ImagePlus, Loader2, Square, X, CornerDownRight, Zap, Pin, PinOff } from "lucide-react";
import { forwardRef, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api, fileRawURL, type Holder, isNetworkError } from "@/lib/api";
import { closeAgentInPane } from "@/lib/actions";
import { get, sendShown, set, setConfig, transcript, useStore } from "@/lib/store";
import type { ContentBlock, SessionSummary } from "@/lib/types";
import { dropShellContext, videoLinkPath } from "@/lib/transcript";
import { MODE, defaultsOf, flatOptions, setDefault, setDefaults } from "@/lib/defaults";
import { isCoarse, usePhone } from "@/lib/phone";
import { askKey } from "@/lib/askkeys";
import { cn } from "@/lib/utils";
import { UsagePanel } from "@/components/UsageMeter";
import { HeldDialog } from "./HeldDialog";
import { PlayBadge } from "./PlayBadge";
import { OptionMenu, OptionsMenu, type OptGroup } from "./OptionMenu";
import { openLightbox } from "../Lightbox";
import { TasksChip } from "./TasksBar";

export type ComposerHandle = {
  focus(): void;
  setText(t: string): void;
  setContent(blocks: ContentBlock[]): void;
  putBack(blocks: ContentBlock[]): void;
  ctrlC(): void;
};

// A video can't go to the agent inline: it's uploaded to the gerdr machine (api.upload) and sent as a resource_link
// to that path. `path` stays unset while the upload is still running; `data` is empty for videos.
type Img = { data: string; mimeType: string; url: string; path?: string; name?: string };
const isVideo = (im: Img) => im.mimeType.startsWith("video/");

function toImgs(blocks: ContentBlock[]): Img[] {
  return blocks.flatMap((c): Img[] => {
    if (c.type === "image") return [{ data: c.data, mimeType: c.mimeType, url: `data:${c.mimeType};base64,${c.data}` }];
    const v = videoLinkPath(c);
    return v && c.type === "resource_link" ? [{ data: "", mimeType: "video/*", url: fileRawURL(v), path: v, name: c.name }] : [];
  });
}

// gerdr 自己接的命令（runLocal）：和 agent 报的同名时用这边的（claude 也报 /usage，发过去会起一轮、进对话记录）
const LOCAL_CMDS = [
  { name: "resume", description: "换到这个目录下之前的一段对话" },
  { name: "usage", description: "看账号的用量额度（不发给 agent）" },
];
// /usage 只有 claude / codex 有额度可看（hub.Usage），别的 agent 不列
const localCmds = (base: string) => LOCAL_CMDS.filter((c) => c.name !== "usage" || base === "claude" || base === "codex");
// 命令别名：agent 报了右边那个命令才有左边的别名，发出去时换回本名（agent 只认本名）
const ALIASES: Record<string, string> = { new: "clear" };
// 每个 pane 输入框的光标 / 选区 / 滚动位置：tab 不保留（keepTabs = 1）时切走就整个卸载，字在 drafts 里还在，
// 光标却跑到了末尾（用户报的）。卸载时记下来，挂回来照着放回去。只放内存：刷新页面不用保住。
const carets = new Map<string, { start: number; end: number; dir: "forward" | "backward" | "none"; top: number }>();
const NO_IMGS: Img[] = [];
const NO_PINS: string[] = []; // 没 pin 过时用同一个空数组，不然每次渲染都是新数组，useMemo 白算

// 补全列表（/ 命令、@ 文件）里选中的那一行：↑↓ 翻到列表外面时把它滚进来（nearest：本来就看得见就不动）。
// 引用换到哪一行，React 就对那一行调一次
const keepInView = (el: HTMLElement | null) => {
  el?.scrollIntoView({ block: "nearest" });
};

export const Composer = forwardRef<
  ComposerHandle,
  {
    session: SessionSummary;
    paneId: string;
    running: boolean;
    onSent?: () => void;
    onEmptyEnter?: () => void;
  }
>(function Composer({ session, paneId, running, onSent, onEmptyEnter }, ref) {
  const text = useStore((s) => s.drafts[paneId] ?? "");
  const sendKey = useStore((s) => s.config.sendKey);
  const pinned = useStore((s) => s.config.pinnedCommands ?? NO_PINS);
  const phone = usePhone();
  // The phone's compact bottom row (options folded into one menu, one shared stop / send button) whenever the full row
  // doesn't fit the box, not just on a phone: a split pane or Work mode's chat column squeezed the dropdowns to "A…" "GP…".
  // Decided by measuring, not a fixed width: how much room the row needs depends on the agent's option names.
  const [narrow, setNarrow] = useState(false);
  const [, setRootW] = useState(0);
  const optsRow = useRef<HTMLDivElement>(null);
  const needW = useRef(0); // the box width the full row needed when it last overflowed
  const compact = phone || narrow;
  const imgs = useStore((s) => s.draftImgs[paneId] ?? NO_IMGS);
  // 读图片的回调可能在切走（卸载）之后才回来，所以每次都从 store 里取最新的再改，别用渲染时的 imgs
  const setImgs = (v: Img[] | ((cur: Img[]) => Img[])) =>
    set((s) => ({ draftImgs: { ...s.draftImgs, [paneId]: typeof v === "function" ? v(s.draftImgs[paneId] ?? NO_IMGS) : v } }));
  const [busy, setBusy] = useState(false);
  const [cmdIdx, setCmdIdx] = useState(0);
  const ta = useRef<HTMLTextAreaElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const imgRefs = useRef<(HTMLDivElement | null)[]>([]);
  const setText = (t: string) => set((s) => ({ drafts: { ...s.drafts, [paneId]: t } }));

  useImperativeHandle(ref, () => ({
    focus: () => ta.current?.focus(),
    setText: (t: string) => putText(t),
    // 回退：整条消息放回来，文字和贴的图都要（只放文字的话截图就丢了）
    setContent: (blocks0: ContentBlock[]) => {
      const blocks = dropShellContext(blocks0); // `!命令` 带过去的输出不放回来：命令卡片还在对话里，下一条照样带上
      putText(blocks.map((c) => (c.type === "text" ? (c as { text: string }).text : "")).join(""));
      setImgs(toImgs(blocks));
    },
    putBack: (blocks: ContentBlock[]) => putBack(blocks),
    ctrlC: () => ctrlC(),
  }));
  // Touch screens: a tap anywhere outside the composer lets go of the text box, so the soft keyboard goes away. Tapping
  // non-focusable things (a <video>'s controls, plain text) doesn't move focus on its own, so the box stayed active and
  // the keyboard kept popping back. Only a tap counts: a drag that turns into a scroll fires pointercancel, and
  // scrolling the chat while typing shouldn't close the keyboard. Taps inside the composer (send, attach…) keep focus.
  useEffect(() => {
    if (!isCoarse()) return;
    let outside = false;
    const down = (e: PointerEvent) => (outside = !root.current?.contains(e.target as Node));
    const cancel = () => (outside = false);
    const up = (e: PointerEvent) => {
      const el = ta.current;
      if (!outside || !el || document.activeElement !== el || root.current?.contains(e.target as Node)) return;
      el.blur();
    };
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("pointercancel", cancel, true);
    document.addEventListener("pointerup", up, true);
    return () => {
      document.removeEventListener("pointerdown", down, true);
      document.removeEventListener("pointercancel", cancel, true);
      document.removeEventListener("pointerup", up, true);
    };
  }, []);
  // 点发送 / 打断按钮不抢输入框的焦点：抢走了，接着按 Esc 打断、回车再发都没反应（用户报的）
  const keepFocus = (e: React.MouseEvent) => {
    e.preventDefault();
    ta.current?.focus();
  };
  const putText = (t: string) => {
    setText(t);
    requestAnimationFrame(() => {
      ta.current?.focus();
      const n = ta.current?.value.length ?? 0;
      ta.current?.setSelectionRange(n, n);
    });
  };

  // 高度跟着内容走，最多到 pane 高度的四成。
  const fitHeight = () => {
    const el = ta.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, Math.max(160, window.innerHeight * 0.4)) + "px";
  };
  useEffect(fitHeight, [text]);
  // And when the width changes: measured while the box was briefly narrow (an overview tile appearing or sliding into
  // place), even the placeholder wraps to many lines and the empty box stayed that tall until the next keystroke.
  useEffect(() => {
    const el = ta.current;
    if (!el) return;
    let w = el.clientWidth;
    const ro = new ResizeObserver(() => {
      if (el.clientWidth === w) return;
      w = el.clientWidth;
      fitHeight();
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Re-render on resize so the check below runs again
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setRootW(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  // Every render (the row also changes with running / the options): full row overflowing → compact, remembering how wide
  // the box would have to be; compact → back to full once the box is that wide again
  useLayoutEffect(() => {
    const o = optsRow.current;
    const r = root.current;
    if (phone || !o || !r || !r.clientWidth) return;
    if (!narrow) {
      const over = o.scrollWidth - o.clientWidth;
      if (over > 1) {
        needW.current = r.clientWidth + over;
        setNarrow(true);
      }
    } else if (r.clientWidth >= needW.current) setNarrow(false);
  });

  useLayoutEffect(() => {
    const el = ta.current;
    if (!el) return;
    const c = carets.get(paneId);
    if (c) {
      el.setSelectionRange(c.start, c.end, c.dir);
      setCaret(c.start);
      // 上面跟内容走的高度是 useEffect，比这里晚（先设成 auto 会把滚动位置冲掉），等它设完再滚
      requestAnimationFrame(() => (el.scrollTop = c.top));
    }
    // 卸载时 ta.current 可能已经清掉了，用挂上时拿到的那个元素：拆下来的 DOM 上选区还读得到
    return () => {
      carets.set(paneId, { start: el.selectionStart, end: el.selectionEnd, dir: el.selectionDirection ?? "none", top: el.scrollTop });
    };
  }, [paneId]);

  const base = session.base ?? session.agent;
  // 斜杠命令：输入框只有一个 /xxx（还没空格）时弹候选。
  const slash = /^\/(\S*)$/.exec(text);
  const cmds = useMemo(() => {
    if (!slash) return [];
    const q = slash[1].toLowerCase();
    // 排序照 Claude Code：名字正好是输入的 → 名字以输入开头的 → 名字里包含的 → 说明里包含的
    const rank = (c: { name: string; description?: string }) => {
      const n = c.name.toLowerCase();
      if (n === q) return 0;
      if (n.startsWith(q)) return 1;
      if (n.includes(q)) return 2;
      if ((c.description ?? "").toLowerCase().includes(q)) return 3;
      return 9;
    };
    const own = session.commands ?? [];
    const local = localCmds(base);
    const aliases = Object.entries(ALIASES)
      .filter(([a, to]) => !own.some((c) => c.name === a) && own.some((c) => c.name === to))
      .map(([a, to]) => ({ name: a, description: `同 /${to}：${own.find((c) => c.name === to)?.description ?? ""}` }));
    return (
      [...local, ...own.filter((c) => !local.some((l) => l.name === c.name)), ...aliases]
        .map((c) => ({ c, r: rank(c), p: pinned.indexOf(c.name) }))
        .filter((x) => x.r < 9)
        // 先看匹配得多准，同一档里 pin 住的排前面（照 pin 的先后），再短的在前。
        // 不能 pin 优先：pin 住的命令只是说明里碰巧带了输入（/res 碰上 /clear 说明里的 resumable）也会压在真正匹配的上面；
        // 只敲一个 / 时全都算「开头匹配」，pin 照样排最上面
        .sort((a, b) => a.r - b.r || (a.p < 0 ? 1e9 : a.p) - (b.p < 0 ? 1e9 : b.p) || a.c.name.length - b.c.name.length)
        .map((x) => x.c)
        .slice(0, 40)
    );
  }, [slash?.[1], session.commands, pinned, base]);
  useEffect(() => setCmdIdx(0), [slash?.[1]]);
  // pin 住的命令：config 里只存名字（所有设备、所有会话共用一份），这里只列这个会话的 agent 真报了的 ——
  // claude 和 codex 的命令不一样，别的 agent 的 pin 在这儿点了也发不出去
  const pinnedCmds = useMemo(() => {
    const own = session.commands ?? [];
    const all = [...localCmds(base), ...own];
    return pinned
      .map(
        (n) => all.find((c) => c.name === n) ?? (ALIASES[n] && own.some((c) => c.name === ALIASES[n]) ? { name: n, description: `同 /${ALIASES[n]}` } : null),
      )
      .filter((c) => !!c);
  }, [pinned, session.commands, base]);
  const togglePin = (name: string) => setConfig({ pinnedCommands: pinned.includes(name) ? pinned.filter((n) => n !== name) : [...pinned, name] });
  // 点了 pin 列表里的一个：本地命令直接做；skill 一般要跟一句话说做什么 —— 填进输入框（已经写了的字接在后面当参数），自己按发送；
  // 别的命令（/compact、/clear……）直接发。直接发不经过输入框：写了一半的草稿留着不动
  const pickPinned = (c: { name: string; _meta?: { skill?: boolean } }) => {
    if (runLocal(c.name)) return;
    // 后端还是没标 skill 的旧版（或者借的是旧版写的命令缓存）：claude 的列表里一个标都没有时分不出来，按 skill 算 —— 宁可多点一下发送，别把 skill 空着发出去
    const own0 = session.commands ?? [];
    const unknown = (session.base ?? session.agent) === "claude" && !own0.some((x) => x._meta);
    if (c._meta?.skill || unknown) {
      const cur = text.trim();
      putText(cur ? `/${c.name} ${cur}` : `/${c.name} `);
      return;
    }
    const own = session.commands ?? [];
    const to = ALIASES[c.name];
    const name = to && !own.some((x) => x.name === c.name) && own.some((x) => x.name === to) ? to : c.name;
    api.send(session.id, [{ type: "text", text: `/${name}` }]).then(
      () => onSent?.(),
      (e: any) => {
        // 会话在别处开着：走和普通发送一样的 HeldDialog（挑完拿输入框里的字重发）—— 输入框空着才借它放命令，草稿不覆盖
        if (e?.code === "held_elsewhere" && !(get().drafts[paneId] ?? "")) {
          setText(`/${name}`);
          setHeld(e.holders ?? []);
          return;
        }
        toast.error(isNetworkError(e) ? `网络断了，/${c.name} 没发出去` : (e.message ?? String(e)));
      },
    );
  };

  // @ 引用文件（照 Claude Code）：光标前面是「@xxx」（@ 在行首或空格后）就弹会话目录下的文件候选，选中换成 @相对路径。
  const [caret, setCaret] = useState(0);
  const [atOff, setAtOff] = useState(-1); // Esc 关掉的那一个 @（同一个位置不再弹）
  const at = useMemo(() => {
    const m = /(^|\s)@([^\s@]*)$/.exec(text.slice(0, caret));
    if (!m) return null;
    const start = caret - m[2].length - 1;
    return start === atOff ? null : { q: m[2], start };
  }, [text, caret, atOff]);
  const [files, setFiles] = useState<string[]>([]);
  const [fileIdx, setFileIdx] = useState(0);
  useEffect(() => {
    if (!at) {
      setFiles([]);
      return;
    }
    const t = setTimeout(() => {
      api
        .searchFiles(session.id, at.q)
        .then((r) => {
          setFiles(r.files);
          setFileIdx(0);
        })
        .catch(() => setFiles([]));
    }, 80);
    return () => clearTimeout(t);
  }, [at?.q, at?.start, session.id]);
  const pickFile = (f: string) => {
    if (!at) return;
    const before = text.slice(0, at.start);
    const ins = `@${f} `;
    setText(before + ins + text.slice(caret));
    // 光标要在下一次重渲染时（同一帧里、下一个按键之前）就挪到插入的内容后面：放进 rAF 的话快手接着打的字会落在路径前面
    caretTo.current = before.length + ins.length;
    setCaret(caretTo.current);
    setFiles([]);
  };
  const caretTo = useRef(-1);
  useLayoutEffect(() => {
    if (caretTo.current < 0) return;
    ta.current?.focus();
    ta.current?.setSelectionRange(caretTo.current, caretTo.current);
    caretTo.current = -1;
  }, [text]);
  // 打了 /，但这个会话的 agent 进程还没起（claude 起来才报命令）：先把进程起起来拿它报的命令。
  // 列表不空也要起 —— 那是借的缓存（commandsCached），可能是升级前的旧版写的，少了新接的命令（codex 的 /clear 就这样没出来过）
  const warmed = useRef(false);
  useEffect(() => {
    if (slash && (!(session.commands ?? []).length || session.commandsCached) && !warmed.current) {
      warmed.current = true;
      api.warm(session.id).catch(() => {});
    }
  }, [!!slash, session.commands?.length, session.commandsCached]);

  // gerdr 自己接的斜杠命令（agent 在 stream-json 模式下不认的那几个）：不发给 agent，在这边做
  const [usage, setUsage] = useState(false); // /usage 的浮层
  const runLocal = (name: string) => {
    if (!localCmds(base).some((c) => c.name === name)) return false;
    setText("");
    if (name === "usage") setUsage(true);
    else set({ dialog: { kind: "resume", sessionId: session.id, paneId } });
    return true;
  };

  // `!命令`（照 Claude Code 的 bash 模式）：gerdr 在会话目录里直接跑，不发给 agent、不起一轮；输出进对话，
  // 跟着下一条消息带给 agent（hub/shell.go）。带图时不算（图没法给命令），照常当话发
  const shellMode = !imgs.length && text.startsWith("!");
  const runShell = async (cmd: string) => {
    if (!cmd) return;
    const prev = text;
    setText("");
    hist.current.idx = -1;
    try {
      await api.shell(session.id, cmd);
      onSent?.();
    } catch (e: any) {
      if (!(get().drafts[paneId] ?? "")) setText(prev);
      toast.error(isNetworkError(e) ? "网络断了，命令没跑（已放回输入框）" : (e.message ?? String(e)));
    }
  };

  // 会话还在别处开着（HeldDialog）：挑完之后拿输入框里的原话再发 —— 用 ref 拿最新的 send（那时 text 已经放回去了）
  const [held, setHeld] = useState<Holder[] | null>(null);
  const sendRef = useRef<() => void>(() => {});

  // now：在跑时立即插话（截断它正在做的这一步）；默认照 Claude Code，排到下一个空档再插进去
  const send = async (now = false) => {
    let t = text.trim();
    if (!imgs.length && /^\/\S+$/.test(t) && runLocal(t.slice(1))) return;
    if (shellMode) return runShell(t.slice(1).trim());
    const alias = /^\/(\S+)(.*)$/s.exec(t);
    const to = alias && ALIASES[alias[1]];
    if (to && !(session.commands ?? []).some((c) => c.name === alias[1]) && (session.commands ?? []).some((c) => c.name === to)) t = `/${to}${alias[2]}`;
    if (!t && !imgs.length) return;
    if (imgs.some((im) => isVideo(im) && !im.path)) {
      toast("视频还在上传，传完再发");
      return;
    }
    const content: ContentBlock[] = [];
    for (const im of imgs)
      content.push(
        im.path
          ? { type: "resource_link", uri: `file://${im.path}`, name: im.name ?? im.path.slice(im.path.lastIndexOf("/") + 1) }
          : { type: "image", data: im.data, mimeType: im.mimeType },
      );
    if (t) content.push({ type: "text", text: t });
    setBusy(true);
    const prev = text;
    const prevImgs = imgs;
    setText("");
    setImgs([]);
    hist.current.idx = -1;
    const tid = `send-${session.id}`;
    try {
      await sendShown(session.id, content, (n) => toast.loading(`网络不好，正在重发（第 ${n} 次）…`, { id: tid }), now);
      toast.dismiss(tid);
      sentAt.current = Date.now();
      if (escPending.current) {
        escPending.current = false;
        escape();
      }
      onSent?.();
    } catch (e: any) {
      toast.dismiss(tid);
      // 没发出去：原样放回输入框（字和图都在），人可以再按一次。
      if (!(get().drafts[paneId] ?? "")) setText(prev);
      setImgs((cur) => (cur.length ? cur : prevImgs));
      if (e?.code === "held_elsewhere") setHeld(e.holders ?? []);
      else toast.error(isNetworkError(e) ? "网络断了，消息没发出去（已放回输入框）" : (e.message ?? String(e)));
    } finally {
      setBusy(false);
    }
  };

  const hist = useRef<{ idx: number; list: string[] }>({ idx: -1, list: [] });
  const sentAt = useRef(0);
  const ctrlCAt = useRef(0);
  const escPending = useRef(false);
  const escaping = useRef(false);
  // ↑ 翻的历史：人说过的话，加上跑过的 `!命令`（照 Claude Code：它们在同一份输入历史里）。命令卡片挂在助手消息里，按出现的先后排进去
  const userTexts = () =>
    transcript(session.id)
      .all.flatMap((m) =>
        m.role === "user"
          ? [m.parts.map((p) => (p.type === "text" ? p.text : "")).join("")]
          : m.parts.flatMap((p) => (p.type === "data-shell" ? ["!" + p.data.command] : [])),
      )
      .filter((t, i, a) => t.trim() && t !== a[i - 1]) // 连着一样的只留一条：`!ls` 跑三遍，↑ 不用按三下
      .reverse();
  // 收回来的消息（Esc / ↑ / 「排队中」那行的撤回）放回输入框：字接在已有的前面，图片也放回来 ——
  // 原来 ↑ 和撤回按钮只放字，截图就丢了（用户报的）
  const putBack = (blocks0: ContentBlock[]) => {
    const blocks = dropShellContext(blocks0); // 同 setContent：`!命令` 的输出不放回输入框
    const back = blocks
      .filter((c) => c.type === "text")
      .map((c) => (c as { text: string }).text)
      .join("\n\n");
    const cur = get().drafts[paneId] ?? "";
    if (back) setText(cur ? `${back}\n\n${cur}` : back);
    const images = toImgs(blocks);
    if (images.length) setImgs((x) => [...images, ...x]);
    ta.current?.focus();
  };
  const recallUp = async () => {
    // 撤回还在路上时再按 ↑ 不管：第二下撤不到东西（第一下已经收走了），会落到翻历史、把同一句话填进输入框，
    // 等第一下回来 putBack 再把它接在前面 —— 输入框里两遍同样的话（用户报的）。和 Esc 的撤回共用一把锁，↑ 和 Esc 也不叠
    if (escaping.current) return;
    if (hist.current.idx < 0 && running) {
      escaping.current = true;
      try {
        const r = await api.withdrawLast(session.id);
        if (r.content?.length) {
          putBack(r.content);
          return;
        }
      } catch {
      } finally {
        escaping.current = false;
      }
    }
    if (hist.current.idx < 0) hist.current.list = userTexts();
    const next = Math.min(hist.current.idx + 1, hist.current.list.length - 1);
    if (next < 0) return;
    hist.current.idx = next;
    setText(hist.current.list[next]);
  };
  const recallDown = () => {
    if (hist.current.idx < 0) return;
    hist.current.idx -= 1;
    setText(hist.current.idx >= 0 ? hist.current.list[hist.current.idx] : "");
  };

  // Esc：和 Claude Code 一样 —— 打断；还没被 agent 用上的话（刚发、它一个字没回 / 排着队的插话）撤回，放回输入框。
  const escape = async () => {
    // 上一次还没回来时连按 Esc（或 ↑ 的撤回还在路上）不再发：几次撤回一起跑，会各自把同一句话还回来，输入框里叠好几遍（踩过）
    if (escaping.current) return;
    escaping.current = true;
    try {
      const r = await api.escape(session.id);
      if (!r.content?.length) return;
      // 不弹提示：字已经回到输入框里了，一看就知道；弹出来的 toast 正好压在输入框右下角挡着操作（用户嫌烦）
      putBack(r.content);
    } catch (err: any) {
      toast.error(err.message);
    } finally {
      escaping.current = false;
    }
  };

  // Ctrl+C（照 Claude Code）：有字先清空；在跑先打断；空着又没在跑 —— 2 秒内连按两次关掉这个 agent，
  // pane 留着回到「新会话」那一屏（再按 Ctrl+D 才关 pane，和终端里退出 shell 一样分两步）。
  // 输入框里按、焦点在对话区（选着字、点过别处）按都走这里，见 ChatView。
  const ctrlC = () => {
    if (text) {
      setText("");
      setImgs([]);
      return;
    }
    if (running) {
      escape();
      return;
    }
    if (Date.now() - ctrlCAt.current < 2000) {
      ctrlCAt.current = 0;
      toast.dismiss("ctrlc");
      closeAgentInPane(paneId);
      return;
    }
    ctrlCAt.current = Date.now();
    toast("再按一次 Ctrl+C 退出这个 agent", { id: "ctrlc", duration: 2000 });
  };

  const addFiles = (files: FileList | File[]) => {
    for (const f of Array.from(files)) {
      if (f.type.startsWith("video/")) {
        addVideo(f);
        continue;
      }
      if (!f.type.startsWith("image/")) continue;
      if (f.size > 20 << 20) {
        toast.error(`${f.name} 太大了（超过 20MB）`);
        continue;
      }
      const r = new FileReader();
      r.onload = () => {
        const url = String(r.result);
        setImgs((xs) => [...xs, { url, data: url.slice(url.indexOf(",") + 1), mimeType: f.type }]);
      };
      r.readAsDataURL(f);
    }
  };

  // Show the video right away from a local object URL, upload it in the background; send waits until it has a path.
  const addVideo = (f: File) => {
    if (f.size > 1 << 30) {
      toast.error(`${f.name} 太大了（超过 1GB）`);
      return;
    }
    const url = URL.createObjectURL(f);
    setImgs((xs) => [...xs, { data: "", mimeType: f.type, url, name: f.name }]);
    api.upload(f).then(
      ({ path }) => setImgs((xs) => xs.map((x) => (x.url === url ? { ...x, path } : x))),
      (e) => {
        toast.error(`${f.name} 没传上去：${e.message ?? e}`);
        setImgs((xs) => xs.filter((x) => x.url !== url));
        URL.revokeObjectURL(url);
      },
    );
  };

  // Enlarge an attachment: images and videos page together in the lightbox, where a video starts playing.
  const preview = (i: number, from: Element) =>
    openLightbox(
      imgs.map((x) => ({ url: x.url, video: isVideo(x) })),
      i,
      from,
    );

  // 选中的那张图上按键：← → 在图之间挑（最后一张再按 → 回输入框），退格 / Delete 删掉它，Esc 回输入框，回车放大看。
  // 删完焦点落到原位置上的下一张（退格往前一张，像删字），一张不剩就回输入框
  const onImgKey = (e: React.KeyboardEvent<HTMLDivElement>, i: number) => {
    if (e.altKey || e.metaKey || e.ctrlKey) return;
    const toText = () => ta.current?.focus();
    if (e.key === "Backspace" || e.key === "Delete") {
      e.preventDefault();
      const n = imgs.length - 1;
      setImgs((xs) => xs.filter((_, j) => j !== i));
      const next = e.key === "Backspace" ? Math.max(i - 1, 0) : Math.min(i, n - 1);
      if (n === 0 || (e.key === "Delete" && i === n)) toText();
      else requestAnimationFrame(() => imgRefs.current[next]?.focus());
    } else if (e.key === "ArrowLeft") {
      e.preventDefault();
      imgRefs.current[Math.max(i - 1, 0)]?.focus();
    } else if (e.key === "ArrowRight") {
      e.preventDefault();
      if (i + 1 < imgs.length) imgRefs.current[i + 1]?.focus();
      else toText();
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      toText();
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      preview(i, e.currentTarget);
    } else if (e.key.length === 1) {
      // 选着图直接打字：回输入框接着打，这个字不丢
      toText();
    }
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // 输入法组字时的回车是给输入法上屏用的（中文用户按回车选词），一律不当发送。
    if (e.nativeEvent.isComposing || e.keyCode === 229) return;
    // agent 在等你回答问题、输入框又空着：↑↓ / 数字 / 回车这些键拿去答题（照 Claude Code），打字照常是直接回复
    if (text === "" && !imgs.length && askKey(session.id, e.nativeEvent)) {
      e.preventDefault();
      return;
    }
    if (at && files.length) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const d = e.key === "ArrowDown" ? 1 : -1;
        setFileIdx((i) => (i + d + files.length) % files.length);
        return;
      }
      if ((e.key === "Tab" && !e.shiftKey) || (e.key === "Enter" && !e.shiftKey)) {
        e.preventDefault();
        pickFile(files[fileIdx] ?? files[0]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setAtOff(at.start);
        return;
      }
    }
    if (cmds.length) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setCmdIdx((i) => (i + 1) % cmds.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setCmdIdx((i) => (i - 1 + cmds.length) % cmds.length);
        return;
      }
      // 照 Claude Code：回车 = 选中高亮的那个并直接发出去（打 /rena 回车就是 /rename）；Tab = 只补全，留个空格接着写参数
      if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
        e.preventDefault();
        const c = cmds[cmdIdx];
        if (!c) return;
        if (runLocal(c.name)) return;
        if (e.key === "Tab") {
          setText(`/${c.name} `);
          return;
        }
        setText(`/${c.name}`);
        setTimeout(() => sendRef.current(), 0); // 等这次 setText 渲染完，sendRef 里的 send 读到的才是补全后的字
        return;
      }
    }
    // ↑ / ↓（照 Claude Code）：输入框空着时按 ↑ —— 有排着队还没被用上的插话就收回来改，没有就翻之前发过的话；
    // 翻历史时 ↓ 往回翻，翻到头清空。输入框里有自己写的字时不管（那是在挪光标）。
    if ((e.key === "ArrowUp" || e.key === "ArrowDown") && !e.shiftKey && !e.altKey && !e.metaKey && !e.ctrlKey) {
      const browsing = hist.current.idx >= 0 && text === hist.current.list[hist.current.idx];
      if (text === "" || browsing) {
        e.preventDefault();
        if (e.key === "ArrowUp") recallUp();
        else recallDown();
        return;
      }
    }
    // 贴的图排在文字前面：光标在最开头（没选中文字）按退格，删掉紧挨着的最后一张图（照 Claude Code），不用伸手去点那个小叉
    if (e.key === "Backspace" && imgs.length && !e.altKey && !e.metaKey && !e.ctrlKey) {
      const el = e.currentTarget;
      if (el.selectionStart === 0 && el.selectionEnd === 0) {
        e.preventDefault();
        setImgs((xs) => xs.slice(0, -1));
        return;
      }
    }
    // 光标已经顶到头还往那个方向按（开头按 ← / ↑、末尾按 → / ↓）：吃掉。浏览器这时什么都不做、事件算「没人处理」，
    // 安卓 WebView 会把它还给系统当方向键导航，焦点被挪出网页，软键盘直接收起（输入法「文字编辑」面板里的方向键就是这么来的）。
    if (/^Arrow(Left|Right|Up|Down)$/.test(e.key) && !e.altKey && !e.metaKey && !e.ctrlKey) {
      const el = e.currentTarget;
      const back = e.key === "ArrowLeft" || e.key === "ArrowUp";
      const edge = back ? 0 : el.value.length;
      if (el.selectionStart === edge && el.selectionEnd === edge) {
        e.preventDefault();
        // 开头再按 ←：选中最后一张图，接着 ← → 挑、退格删。触屏不挪：焦点离开输入框软键盘就收了
        if (e.key === "ArrowLeft" && imgs.length && !e.shiftKey && !isCoarse()) imgRefs.current[imgs.length - 1]?.focus();
        return;
      }
    }
    // Shift+Tab（照 Claude Code）：轮换权限模式
    if (e.key === "Tab" && e.shiftKey && !e.metaKey && !e.ctrlKey && !e.altKey) {
      // 「全放开」那几档不进轮换（Claude Code 也是）：一是按快了一下子越过审批太危险；二是 claude 没带
      // --dangerously-skip-permissions 起的进程根本切不过去，切了只会报错。要用就在下拉框里明确地选。
      const list = (session.modes?.availableModes ?? []).filter((m) => !/bypass|full-access|dontAsk/i.test(m.id));
      if (list.length > 1) {
        e.preventDefault();
        const i = list.findIndex((m) => m.id === session.modes?.currentModeId);
        const next = list[(i + 1) % list.length];
        api.setMode(session.id, next.id).catch((err) => toast.error(err.message));
        toast(`模式：${next.name}`, { id: "mode", duration: 1500 });
      }
      return;
    }
    // 只认 ctrl，不认 ⌘：mac 上 ⌘C 是复制。
    if (e.key === "c" && e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) {
      e.preventDefault();
      ctrlC();
      return;
    }
    // Esc 按得快的两种：消息还在发（等发完再撤）、刚发完界面还没收到「在跑」（服务端其实已经在跑了）
    if (e.key === "Escape" && (running || busy || Date.now() - sentAt.current < 5000)) {
      e.preventDefault();
      if (busy) escPending.current = true;
      else escape();
      return;
    }
    if (e.key === "Enter") {
      const mod = e.metaKey || e.ctrlKey;
      // 触屏的软键盘上回车就是换行（没有 Shift 可按），发送走右下角那个按钮
      const wantSend = sendKey === "enter" && !isCoarse() ? !e.shiftKey && !e.altKey : mod;
      if (wantSend) {
        e.preventDefault();
        // Nothing to send: Enter jumps the chat to the bottom instead, so you don't have to reach for the ↓ button
        if (!text.trim() && !imgs.length) return onEmptyEnter?.();
        // 在跑时 Ctrl/⌘+回车 = 立即插话（照 Claude Code CLI）。发送键设成 ⌘+回车时这个组合就是普通发送，立即只能点按钮
        if (!busy) send(running && sendKey === "enter" && mod);
      }
    }
  };

  const modes = session.modes;
  const opts = session.configOptions ?? [];
  // 新会话的默认值：订阅这几样，设完下拉里马上显示「已是默认」
  useStore((s) => s.config.defaultModes);
  useStore((s) => s.config.defaultConfig);
  useStore((s) => s.config.llm);
  const defs = defaultsOf(session.agent);
  const hasText = text.trim().length > 0 || imgs.length > 0;
  const modeGroup: OptGroup | undefined = modes?.availableModes.length
    ? {
        id: MODE,
        label: "模式",
        value: modes.currentModeId,
        defaultValue: defs[MODE],
        options: modes.availableModes.map((m) => ({ value: m.id, name: m.name, description: m.description })),
        onChange: (v) => api.setMode(session.id, v).catch((e) => toast.error(e.message)),
      }
    : undefined;
  const optGroups: OptGroup[] = opts.map((o) => ({
    id: o.id,
    label: o.name,
    value: o.currentValue,
    defaultValue: defs[o.id],
    options: flatOptions(o),
    onChange: (v) => api.setConfig(session.id, o.id, v).catch((e) => toast.error(e.message)),
  }));

  sendRef.current = send;
  return (
    <div ref={root} className="relative px-3 pb-3">
      {usage && <UsagePanel agent={base} onClose={() => setUsage(false)} />}
      <HeldDialog sessionId={session.id} holders={held} onClose={() => setHeld(null)} onDone={() => setTimeout(() => sendRef.current(), 50)} />
      {slash && !(session.commands ?? []).length && (
        <div className="absolute bottom-full left-3 right-3 z-20 mb-1 rounded-lg border bg-popover px-3 py-2 text-[12px] text-muted-foreground shadow-lg">
          正在加载命令…（{(session.base ?? session.agent) === "claude" ? "Claude Code 起来之后才报命令列表" : "这个 agent 没有报命令"}）
        </div>
      )}
      {at && files.length > 0 && (
        <div className="absolute bottom-full left-3 right-3 z-20 mb-1 max-h-64 overflow-auto rounded-lg border bg-popover p-1 shadow-lg">
          {files.map((f, i) => {
            const cut = f.lastIndexOf("/");
            return (
              <button
                key={f}
                className={cn("flex w-full items-baseline gap-2 rounded-sm px-2 py-1 text-left text-[13px]", i === fileIdx && "bg-accent")}
                ref={i === fileIdx ? keepInView : undefined}
                onMouseEnter={() => setFileIdx(i)}
                onMouseDown={(e) => {
                  e.preventDefault();
                  pickFile(f);
                }}
              >
                <span className="shrink-0 font-mono">{f.slice(cut + 1)}</span>
                <span className="truncate text-[12px] text-muted-foreground">{cut > 0 ? f.slice(0, cut) : ""}</span>
              </button>
            );
          })}
        </div>
      )}
      {cmds.length > 0 && (
        <div className="absolute bottom-full left-3 right-3 z-20 mb-1 max-h-64 overflow-auto rounded-lg border bg-popover p-1 shadow-lg">
          {cmds.map((c, i) => (
            <button
              key={c.name}
              className={cn("flex w-full items-baseline gap-2 rounded-sm px-2 py-1 text-left text-[13px]", i === cmdIdx && "bg-accent")}
              ref={i === cmdIdx ? keepInView : undefined}
              onMouseEnter={() => setCmdIdx(i)}
              onMouseDown={(e) => {
                e.preventDefault();
                if (runLocal(c.name)) return;
                setText(`/${c.name} `);
              }}
            >
              <span className="shrink-0 font-mono">/{c.name}</span>
              <span className="min-w-0 flex-1 truncate text-[12px] text-muted-foreground">{c.description}</span>
              {/* pin 开关：按钮里不能再套按钮，用 span 接 mousedown（和整行一样用 mousedown，免得输入框先失焦、列表跟着关了） */}
              <span
                role="button"
                title={pinned.includes(c.name) ? "取消 pin" : "pin 到输入框（附图旁边的 / 按钮）"}
                className={cn(
                  "shrink-0 self-center rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground",
                  pinned.includes(c.name) ? "text-brand" : i === cmdIdx ? "opacity-100" : "opacity-0 [@media(hover:none)]:opacity-60",
                )}
                onMouseDown={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  togglePin(c.name);
                }}
              >
                {pinned.includes(c.name) ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
              </span>
            </button>
          ))}
        </div>
      )}
      <div
        className={cn(
          // 照 Astryx ChatComposer：大圆角 + 投影托起来，描边只留一根细线（浅色底上要靠它看出边界）
          "rounded-2xl border border-border/60 bg-card shadow-md transition-colors focus-within:border-ring/60",
          running && "border-brand/40",
          shellMode && "border-warn/60 focus-within:border-warn/80",
        )}
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          if (e.dataTransfer.files.length) {
            e.preventDefault();
            addFiles(e.dataTransfer.files);
          }
        }}
      >
        {imgs.length > 0 && (
          <div className="flex flex-wrap gap-2 px-3 pt-3">
            {imgs.map((im, i) => (
              <div
                key={i}
                ref={(el) => {
                  imgRefs.current[i] = el;
                }}
                tabIndex={-1}
                onKeyDown={(e) => onImgKey(e, i)}
                className="group/img relative rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-card"
              >
                {isVideo(im) ? (
                  <div onClick={(e) => preview(i, e.currentTarget)} className="relative size-14 cursor-zoom-in overflow-hidden rounded-md border bg-muted">
                    <video src={`${im.url}#t=0.1`} muted playsInline preload="metadata" className="pointer-events-none size-full object-cover" />
                    {im.path ? (
                      <PlayBadge small />
                    ) : (
                      <div className="absolute inset-0 flex items-center justify-center bg-background/40 text-foreground">
                        <Loader2 className="size-5 animate-spin" />
                      </div>
                    )}
                  </div>
                ) : (
                  <img src={im.url} onClick={(e) => preview(i, e.currentTarget)} className="size-14 cursor-zoom-in rounded-md border object-cover" />
                )}
                <button
                  className="absolute -right-1.5 -top-1.5 hidden rounded-full bg-foreground p-0.5 text-background group-hover/img:block [@media(hover:none)]:block"
                  onClick={() => setImgs(imgs.filter((_, j) => j !== i))}
                >
                  <X className="size-3" />
                </button>
              </div>
            ))}
          </div>
        )}
        <textarea
          ref={ta}
          data-composer={paneId}
          rows={1}
          value={text}
          onChange={(e) => {
            // 中文输入法下按 / 出来的是「、」（有的是全角「／」）：空输入框打的第一个字是它，就当 / —— 不然想调命令得先切英文
            // ！ 同理：中文输入法下打出来的是全角的，当 ! 用（`!命令`）
            const v = text === "" && /^[、／]$/.test(e.target.value) ? "/" : text === "" && e.target.value === "！" ? "!" : e.target.value;
            setText(v);
            setCaret(e.target.selectionStart ?? e.target.value.length);
          }}
          onSelect={(e) => setCaret(e.currentTarget.selectionStart ?? 0)}
          onKeyDown={onKeyDown}
          onPaste={(e) => {
            const files = Array.from(e.clipboardData.files);
            if (files.some((f) => f.type.startsWith("image/") || f.type.startsWith("video/"))) {
              e.preventDefault();
              addFiles(files);
            }
          }}
          placeholder={
            running
              ? phone
                ? "它在干活：说的话等这一步做完插进去"
                : sendKey === "enter"
                  ? "它在干活：↵ 等这一步做完插进去，Ctrl+↵ 立即插"
                  : "它在干活：说一句会等这一步做完插进去"
              : "说点什么…  / 看命令  ! 跑命令"
          }
          className="block max-h-[40vh] min-h-[44px] w-full resize-none bg-transparent px-4 pt-3 pb-1 text-[1em] max-md:text-[length:max(1em,15px)] max-md:supports-[-webkit-touch-callout:none]:text-[16px] leading-relaxed outline-none placeholder:text-muted-foreground/70"
        />
        {shellMode && (
          // 照 Claude Code 的「! for bash mode」：说清这一句不发给 agent、在哪个目录跑、输出去哪
          <div className="truncate px-4 pb-1 text-[12px] text-warn" title={session.cwd}>
            命令模式：在 {session.cwd.replace(/^\/Users\/[^/]+/, "~")} 里直接跑，不经过 agent；输出跟着你的下一条消息带给它
          </div>
        )}
        <div className="flex items-center gap-1 px-2.5 pb-2.5">
          {/* 附图和几个下拉放一组：放不下时这一组横着滑，而不是每个都被挤成「A…」「Opus …」（用户报的）；
              手机上模式 / 模型 / 思考强度收成一个图标（OptionsMenu），平时几乎不改，不值得常驻。上下文 / 发送这些固定在右边不动 */}
          <div ref={optsRow} className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto [scrollbar-width:none] [&>*]:shrink-0">
            <Tooltip>
              <TooltipTrigger asChild>
                <label className="flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground">
                  <ImagePlus className="size-4" />
                  <input
                    type="file"
                    accept="image/*,video/*"
                    multiple
                    className="hidden"
                    onChange={(e) => {
                      if (!e.target.files?.length) return;
                      addFiles(e.target.files);
                      // 触屏上选完图直接弹键盘：附了图基本都要配一句话，省得再点一下输入框。
                      // 要在 change 回调里同步聚焦 —— 选图还算这次点击，晚了（等读完图）浏览器不给弹键盘
                      if (isCoarse()) ta.current?.focus();
                    }}
                  />
                </label>
              </TooltipTrigger>
              <TooltipContent>附图片 / 视频（也可以直接粘贴 / 拖进来）</TooltipContent>
            </Tooltip>
            {pinnedCmds.length > 0 && (
              <DropdownMenu>
                <DropdownMenuTrigger className="flex size-7 shrink-0 items-center justify-center rounded-md font-mono text-[14px] text-muted-foreground outline-none hover:bg-muted hover:text-foreground data-[state=open]:bg-muted">
                  /
                </DropdownMenuTrigger>
                {/* 关掉时不把焦点还给触发器：pickPinned 要把焦点放进输入框（手机上好直接弹键盘接着写参数） */}
                <DropdownMenuContent side="top" align="start" className="max-h-80 w-72 overflow-auto" onCloseAutoFocus={(e) => e.preventDefault()}>
                  {pinnedCmds.map((c) => (
                    <DropdownMenuItem key={c.name} onClick={() => pickPinned(c)} className="items-baseline gap-2">
                      <span className="shrink-0 font-mono text-[13px]">/{c.name}</span>
                      <span className="truncate text-[12px] text-muted-foreground">{c.description}</span>
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
            {compact ? (
              <OptionsMenu
                mode={modeGroup}
                groups={optGroups}
                onSetDefault={() => {
                  setDefaults(session.agent, Object.fromEntries([modeGroup, ...optGroups].filter((g) => !!g).map((g) => [g!.id, g!.value])));
                  toast.success("之后新开的会话按这个来");
                }}
              />
            ) : (
              [modeGroup, ...optGroups].map(
                (g) =>
                  g && (
                    <OptionMenu
                      key={g.id}
                      label={g.label}
                      value={g.value}
                      options={g.options}
                      onChange={g.onChange}
                      defaultValue={g.defaultValue}
                      onSetDefault={() => {
                        setDefault(session.agent, g.id, g.value);
                        toast.success(`之后新开的会话${g.label}默认用这个`);
                      }}
                    />
                  ),
              )
            )}
          </div>
          <TasksChip session={session} />
          {session.context && <ContextMeter c={session.context} short={compact} />}
          {/* 手机上底栏放不下「打断 / 立即发 / 发送」三个圆钮（再挤下拉就又被截了）：照 ChatGPT 共用一个位置 ——
              在跑、没打字时是打断；打了字是立即发 + 插进去。电脑上地方够，三个都摆着 */}
          {running && (!compact || !hasText) && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button size="icon" variant={compact ? "default" : "ghost"} className="size-8 rounded-full" onMouseDown={keepFocus} onClick={() => escape()}>
                  <Square className="size-3.5 fill-current" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>打断（Esc）</TooltipContent>
            </Tooltip>
          )}
          {running && (!compact || hasText) && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 rounded-full"
                  disabled={!hasText || busy}
                  onMouseDown={keepFocus}
                  onClick={() => send(true)}
                >
                  <Zap className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>立即发送：打断它正在做的这一步{sendKey === "enter" ? "（Ctrl+↵）" : ""}</TooltipContent>
            </Tooltip>
          )}
          {(!compact || !running || hasText) && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button size="icon" className="size-8 rounded-full" disabled={!hasText || busy} onMouseDown={keepFocus} onClick={() => send()}>
                  {running ? <CornerDownRight className="size-4" /> : <ArrowUp className="size-4" />}
                </Button>
              </TooltipTrigger>
              <TooltipContent>{running ? "等它做完这一步再插进去" : "发送"}</TooltipContent>
            </Tooltip>
          )}
        </div>
      </div>
    </div>
  );
});

/** 上下文用了多少（照 Claude Code 的「Context left until auto-compact」）：过半才显眼，快满变色。 */
function ContextMeter({ c, short }: { c: { tokens: number; window: number }; short?: boolean }) {
  const pct = Math.min(100, Math.round((c.tokens / c.window) * 100));
  const tone = pct >= 90 ? "text-bad" : pct >= 75 ? "text-warn" : "text-muted-foreground";
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={cn("mr-1 shrink-0 whitespace-nowrap text-[12px] tabular-nums", tone)}>
          {/* Compact row (phone, or a narrow box): just the percentage, the label would get squeezed to one character a line */}
          {!short && "上下文 "}
          {pct}%
        </span>
      </TooltipTrigger>
      <TooltipContent>
        已用 {c.tokens.toLocaleString()} / {c.window.toLocaleString()} tokens。快满时 agent 会自动压缩，也可以 /compact 手动压。
      </TooltipContent>
    </Tooltip>
  );
}

export function draftOf(paneId: string) {
  return get().drafts[paneId] ?? "";
}
