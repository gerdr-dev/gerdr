// 急救面板的中英文。语言和主界面同一个设置：localStorage 的 "gerdr.language"（"zh" | "en"，没有就跟系统走：
// navigator.languages[0] 以 zh 开头是中文，否则英文）。面板由页面外壳直接引入、和主界面同源，localStorage 是同一份。
//
// 故意不 import ui/src/lib/i18n.ts：界面目录是会被改坏的，急救面板不能和它有牵连（见 main.tsx）。所以这里抄一份最小的：
// 读一次语言（主界面改语言会刷新页面，这里跟着重新读）、t(key, vars) 填 {name} 占位符。
// 新加的字放进下面两张表，en 由类型检查保证和 zh 的 key 一一对应。

export type Locale = "zh" | "en";

const KEY = "gerdr.language";

function pick(): Locale {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "zh" || v === "en") return v;
  } catch {}
  const langs = navigator.languages?.length ? navigator.languages : [navigator.language];
  return (langs[0] ?? "").toLowerCase().startsWith("zh") ? "zh" : "en";
}

const current: Locale = pick();

/** 面板现在的语言 */
export const locale = (): Locale => current;

const zh = {
  // 面板本体（Panel.tsx）
  "panel.pill.title": "界面报错了：打开急救面板，叫 agent 来修",
  "panel.name": "急救",
  "panel.dismiss.title": "忽略这些错误",
  "panel.subtitle": "内置在 Gerdr 里，主界面怎么改坏它都在",
  "panel.refresh": "重新读一遍错误和历史",
  "panel.close": "关闭急救面板",
  "panel.upgrade.title": "有新版界面没合上",
  "panel.upgrade.hint": "开个新对话，让 agent 把新版合进来、两边的改动都留着。",
  "panel.tab.chat": "叫 agent 修",
  "panel.tab.history": "回到之前的一版",
  "panel.helloErr": "连不上 Gerdr：{error}",
  "panel.loading": "读取中…",
  "status.err": "读不到界面状态：{error}",
  "status.ok": "界面现在没报错",
  "status.okHint": "运行时错误要主界面开着才报得上来",
  "status.dirty": " · {n} 个文件改了还没提交",
  "status.bad": "界面报错了",
  "status.dismiss.title": "临时的错、已经修好了的错：清掉",
  "status.dismiss": "忽略",
  "status.collapse": "收起",
  "status.expand": "展开",
  "versions.restored": "已回到 {sha}。左边的主界面会跟着变，没变就刷新一下。",
  "versions.restoredSaved": "已回到 {sha}，之前的样子存成了 {saved}。左边的主界面会跟着变，没变就刷新一下。",
  "versions.failed": "回退失败：{error}",
  "versions.hint": "先让主界面能用上，再慢慢修。回退前现在的样子会先存一版，改动不会丢，还能再回来。",
  "versions.empty": "没有历史",
  "versions.current": "现在",
  "versions.upstream": "Gerdr 原版",
  "versions.restoring": "回退中…",
  "versions.confirm": "确定？再点一次",
  "versions.restore": "回到这一版",

  // 对话（Chat.tsx）
  "chat.status.idle": "空闲",
  "chat.status.running": "在干活…",
  "chat.status.waiting": "等你处理",
  "chat.status.error": "出错了",
  "chat.status.starting": "启动中…",
  "chat.title.merge": "急救：合入新版界面",
  "chat.title.fix": "急救：修界面",
  // 新对话预填给 agent 的第一句
  "chat.prompt.mergeHow":
    "运行 `gerdr ui merge` 把冲突摆出来，逐个解决：新版的改动和本地的改动都要保留（看不懂本地那边为什么这么改，先看 git log）。" +
    "解决完 `git add -A && git commit`，再跑 `gerdr ui status`，确认没有错误、也没有 pending_upstream 了。",
  "chat.prompt.merge": "Gerdr 出了新版界面，但和这个目录里本地的改动冲突，自动升级时没合进来。先读 AGENTS.md，然后",
  "chat.prompt.fix":
    "Gerdr 的界面（就是这个目录）被改坏了，帮我修好。先读 AGENTS.md，再跑 `gerdr ui status` 看错误；修完再跑一次确认没有错误了。只改坏掉的地方，别顺手重构。",
  "chat.prompt.errors": "\n\n现在报的错：\n",
  "chat.prompt.alsoUpgrade": "\n\n修好之后还有一件：{upgrade}。",
  "chat.createFailed": "开会话失败：{error}",
  "chat.sendFailed": "发送失败：{error}",
  "chat.continuing": "{agent} · 接着上次的对话",
  "chat.noSession": "还没开过急救对话",
  "chat.agent.title": "新对话用哪个 agent",
  "chat.new.title": "在界面目录里开一个新对话，错误信息自动带上",
  "chat.new": "新对话",
  "chat.statusSep": "：",
  "chat.placeholder": "告诉 agent 哪里坏了（⌘/Ctrl + Enter 发送）",
  "chat.placeholderNone": "先开一个新对话",
  "chat.stop": "打断",
  "chat.send": "发送",
  "chat.empty.title": "开一个新对话",
  "chat.empty.body": "agent 在界面目录里干活，上面的错误会自动带给它。修的时候左边的主界面跟着变。",
  "chat.noMessages": "还没有消息",
  "chat.connecting": "连接中…",
  "chat.tool": "工具调用",
  "chat.perm.title": "要你批准",
  "chat.perm.done": "批准",
  "chat.perm.cancelled": "已取消",
  "chat.perm.ask": "要你批准：",
  "chat.ask.noAnswer": "（没回答）",
  "chat.ask.other": "或者自己写",
  "chat.ask.submit": "提交",
  "chat.ask.skip": "跳过",

  // api.ts
  "ago.now": "刚刚",
  "ago.min": "{n} 分钟前",
  "ago.hour": "{n} 小时前",
  "ago.day": "{n} 天前",
  "err.build": "编译错误：\n",
  "err.runtime": "运行时错误（{source}）：{message}",
  "err.merging": "界面目录正合并到一半，有冲突：",
  "upgrade.text": "Gerdr 的新版界面{version}和本地对界面的改动有冲突，自动升级时没合进来，现在用的还是旧的。{files}",
  "upgrade.version": "（{v}）",
  "upgrade.files": "会冲突的文件：{files}",
};

export type Key = keyof typeof zh;

const en: Record<Key, string> = {
  "panel.pill.title": "The UI hit an error: open the rescue panel and have an agent fix it",
  "panel.name": "Rescue",
  "panel.dismiss.title": "Ignore these errors",
  "panel.subtitle": "Built into Gerdr: still here however broken the main UI gets",
  "panel.refresh": "Reload errors and history",
  "panel.close": "Close the rescue panel",
  "panel.upgrade.title": "A new UI version wasn't merged",
  "panel.upgrade.hint": "Start a new chat and have the agent merge it in, keeping the changes on both sides.",
  "panel.tab.chat": "Ask an agent to fix",
  "panel.tab.history": "Go back to an earlier version",
  "panel.helloErr": "Can't reach Gerdr: {error}",
  "panel.loading": "Loading…",
  "status.err": "Can't read the UI status: {error}",
  "status.ok": "No UI errors right now",
  "status.okHint": "Runtime errors are only reported while the main UI is open",
  "status.dirty": " · {n} changed files not committed",
  "status.bad": "The UI has errors",
  "status.dismiss.title": "Clear temporary or already fixed errors",
  "status.dismiss": "Ignore",
  "status.collapse": "Less",
  "status.expand": "More",
  "versions.restored": "Back at {sha}. The main UI on the left will follow; reload if it doesn't.",
  "versions.restoredSaved": "Back at {sha}; the previous state was saved as {saved}. The main UI on the left will follow; reload if it doesn't.",
  "versions.failed": "Restore failed: {error}",
  "versions.hint": "Get the main UI working first, fix it later. The current state is saved before going back, so nothing is lost and you can return to it.",
  "versions.empty": "No history",
  "versions.current": "Current",
  "versions.upstream": "Gerdr original",
  "versions.restoring": "Restoring…",
  "versions.confirm": "Sure? Click again",
  "versions.restore": "Restore this version",

  "chat.status.idle": "Idle",
  "chat.status.running": "Working…",
  "chat.status.waiting": "Needs you",
  "chat.status.error": "Error",
  "chat.status.starting": "Starting…",
  "chat.title.merge": "Rescue: merge the new UI",
  "chat.title.fix": "Rescue: fix the UI",
  "chat.prompt.mergeHow":
    "run `gerdr ui merge` to lay out the conflicts and resolve them one by one, keeping both the new version's changes and the local ones (if it's unclear why the local side changed something, check git log first). " +
    "When done, `git add -A && git commit`, then run `gerdr ui status` and make sure there are no errors and no pending_upstream.",
  "chat.prompt.merge":
    "Gerdr has a new UI version, but it conflicts with local changes in this directory, so the automatic upgrade didn't merge it. Read AGENTS.md first, then ",
  "chat.prompt.fix":
    "Gerdr's UI (this directory) is broken; please fix it. Read AGENTS.md first, then run `gerdr ui status` to see the errors; " +
    "run it again afterwards to confirm they're gone. Only fix what's broken, no refactoring along the way.",
  "chat.prompt.errors": "\n\nCurrent errors:\n",
  "chat.prompt.alsoUpgrade": "\n\nOne more thing once it's fixed: {upgrade} To merge it, ",
  "chat.createFailed": "Couldn't start a chat: {error}",
  "chat.sendFailed": "Send failed: {error}",
  "chat.continuing": "{agent} · continuing the last chat",
  "chat.noSession": "No rescue chat yet",
  "chat.agent.title": "Agent for new chats",
  "chat.new.title": "Start a new chat in the UI directory, with the errors included",
  "chat.new": "New chat",
  "chat.statusSep": ": ",
  "chat.placeholder": "Tell the agent what's broken (⌘/Ctrl + Enter to send)",
  "chat.placeholderNone": "Start a new chat first",
  "chat.stop": "Stop",
  "chat.send": "Send",
  "chat.empty.title": "Start a new chat",
  "chat.empty.body": "The agent works in the UI directory and gets the errors above automatically. The main UI on the left changes as it fixes.",
  "chat.noMessages": "No messages yet",
  "chat.connecting": "Connecting…",
  "chat.tool": "Tool call",
  "chat.perm.title": "Needs your approval",
  "chat.perm.done": "Approve",
  "chat.perm.cancelled": "Cancelled",
  "chat.perm.ask": "Needs your approval: ",
  "chat.ask.noAnswer": "(no answer)",
  "chat.ask.other": "Or write your own",
  "chat.ask.submit": "Submit",
  "chat.ask.skip": "Skip",

  "ago.now": "just now",
  "ago.min": "{n} min ago",
  "ago.hour": "{n} h ago",
  "ago.day": "{n} d ago",
  "err.build": "Build error:\n",
  "err.runtime": "Runtime error ({source}): {message}",
  "err.merging": "The UI directory is in the middle of a merge, with conflicts: ",
  "upgrade.text":
    "A new Gerdr UI{version} conflicts with your local changes to the UI, so the automatic upgrade didn't merge it; the old one is still in use.{files}",
  "upgrade.version": " ({v})",
  "upgrade.files": " Conflicting files: {files}.",
};

/** key 在面板语言里的字，{name} 占位符用 vars 填 */
export function t(key: Key, vars?: Record<string, string | number>): string {
  let s = (current === "en" ? en : zh)[key] ?? zh[key] ?? key;
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.split(`{${k}}`).join(String(v));
  return s;
}
