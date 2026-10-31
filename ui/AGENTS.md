# gerdr 的界面

这个目录就是 gerdr 的整个界面，**没有打包环节**：gerdr 直接读这里的源码，每个文件用 esbuild 单独编译后发给浏览器，
保存即生效（React Refresh 热替换，页面状态尽量保住）。不用 `npm install`、不用 build、不用重启 gerdr。

界面目录是 git 仓库：`upstream` 分支是 gerdr 发的版本，`main` 是正在用的；gerdr 升级时把新版提交到 `upstream` 再合进 `main`
（三方合并，你的改动保留）。只在 `main` 上改、别动 `upstream`。改之前先存个档，改坏了能回去：

```bash
git -C <界面目录> add -A && git -C <界面目录> commit -qm "改界面之前" || true
```

主界面改坏了用户也不至于没辙：页面右侧能拉出编进 gerdr 的急救面板（不在这个目录里，改不到），能看错误、退回之前的一版、叫 agent 来修。

改完查一下有没有改坏（编译错误、页面里冒出来的运行时错误都在里面；页面开着才有运行时错误）：

```bash
gerdr ui status
```

That only shows the errors of the version on screen now. Errors from earlier, e.g. ones the user dismissed or rolled back
in the rescue panel, are in `~/.gerdr/logs/gerdr.log`: `grep -E 'ui client error|ui build failed|ui restored|ui errors dismissed'`.

## 目录

```
package.json       dependencies：界面用到的第三方包和版本（只有这里写了的包才能 import）
ui.json            标题、图标
src/index.css      样式：主题色变量（:root / .dark）、Tailwind 的 @theme
src/pages/         页面，react-router 按文件路由：index.tsx → /，usage.tsx → /usage，s/[id].tsx → /s/:id
src/layout.tsx     （可选）所有页面外面那一层，里面放 <Outlet />
src/App.tsx        首页的工作台：侧栏、tab 栏、分屏
src/components/    组件
  ui/              shadcn/ui 组件（Button、Dialog、Select……），新组件照它们的写法
  ai-elements/     对话流的组件（AI SDK 的 AI Elements）
  chat/            gerdr-only pieces of the chat: ChatView (wraps the package's, tells it whether the tab shows), RunButton (`! command` in a reply),
                   HeldDialog (session open elsewhere)
  Chrome.tsx       侧栏、tab 栏、手机上那一行顶栏（PhoneBar）
  Panes.tsx        分屏和每个 pane 的标题栏（手机上只画焦点那一个 pane）
  Terminal.tsx     终端 pane 的内容（xterm.js，连服务端的 PTY；xterm 的样式在 index.css 末尾）
  PaneSwitcher.tsx 手机上的「面板一览」：所有 pane 拍平，点一下就过去
  FileViewer.tsx   对话里点了路径之后的查看器（图片 / 视频 / 音频 / PDF / 文本）
  Dialogs.tsx      所有弹窗挂在这一处（store 里的 dialog 决定开哪个）；每个弹窗一个文件：
                   Palette（命令面板）、KeyHelp（快捷键表）、SettingsDialog（设置）、Pickers（会话列表 / 切工作空间）、
                   NameDialogs（重命名、新 session、新工作空间）
  ExtSettings.tsx  设置里的 Skill / MCP 两页
packages/chat/     the conversation UI itself, a package (@gerdr/chat) also published to npm for other apps. Served from source like src/:
  src/components/  ChatView (the whole panel, virtual list), Rows (the conversation as rows), MessageView (user messages / how each block
                   draws), Composer (input box), ToolCall (tool call cards), Cards (permission / question / plan), TasksBar (background tasks)
  src/host.tsx     ChatHost: everything the chat gets from the app (data, actions, slots). gerdr's is src/lib/chatHost.tsx
  src/messages.ts  every text the chat shows, zh + en: add new texts here, not inline
  src/style.css    the chat's CSS (Markdown typography…); gerdr adds it after src/index.css
  Imports inside the package are relative (no @/…), and it can't import gerdr's src/: what it needs from gerdr goes through ChatHost.
src/lib/
  chatHost.tsx     gerdr's ChatHost (store, api, actions, and the composer features only gerdr has)
  api.ts           调 gerdr 后端的 HTTP 接口（所有接口都在这里有一个函数）
  store.ts         全局状态（zustand）：sessions、layout、config、对话（transcript）
  sync.ts          WebSocket：服务端推来的会话变化、日志
  transcript.ts    re-exports packages/chat/src/transcript.ts (会话日志 → AI SDK 的 UIMessage)
  types.ts         后端数据的类型（SessionSummary、HubEvent……）
  actions.ts       分屏、开会话、切 tab 这些动作
  keys.ts          快捷键
  phone.ts         手机那一档（usePhone，< 768px）、--vvh（软键盘弹起时的可视高度）
  addons.ts        the add-on runtime (~/.gerdr/addons); Settings → Add-ons is components/AddonSettings.tsx
```

## 规矩（编译器就是这么认的）

- `@/x` 指 `src/x`。相对路径也行。
- 第三方包：从 npm 包名 import（`import { X } from "lucide-react"`），**包必须写在 package.json 的 dependencies 里，写精确版本**
  （`"recharts": "2.15.4"`，不要 `^`）。加了新包不用安装，浏览器从 esm.sh 拉（gerdr 本机缓存）。
  能不加新包就不加：图标用 lucide-react，组件优先用 src/components/ui 里现成的。
- **不能在代码里 import CSS**、不能 `?raw` / `?url`。样式用 Tailwind class 写在组件上；全局的写进 `src/index.css`。
- 样式是浏览器里的 Tailwind v4（Tailwind browser）：任何 class 都直接生效；`src/index.css` 里不能 `@import` / `@plugin`。
- 颜色用主题变量对应的 class（`bg-background`、`text-muted-foreground`、`border`、`bg-brand`、`text-ok`、`text-bad`、`text-warn`），
  不要写死颜色 —— 深浅色两套都要好看。
- 圆角用 `rounded-sm/md/lg/xl` 这几档（哪种东西用哪档见 `src/index.css` 里 `--radius` 上面的注释），别用裸 `rounded` 或写死像素。
- 一个 `.tsx` 文件里只导出组件（大写开头 / default）时，改它能热替换、不丢状态；导出了别的（函数、常量）的文件改了会整个重新渲染。

- 对话是**虚拟列表**（react-virtuoso）：「设置 → 实验室」里打开虚拟滚动时，只渲染屏幕附近的行，滚出去的会被卸载（默认关着，但组件要按开着写）。所以对话里的组件**别用 useState 记「展开没」这类状态**
  （滚回来就没了），用 `packages/chat/src/kept.ts` 的 `useKept(key, 初值)`，key 带上会话 id 和这块东西的 id。
- 格式按 `.prettierrc.json`（行宽 160、双引号、分号），`src/` 下的 ts / tsx 已经全部是 prettier 3.9.10 排过的：改完对改了的文件跑
  `npx prettier@3.9.10 --write <文件>`，**要在界面目录里跑** —— 在别处跑（或者把文件拷到 /tmp 再排）读不到 `.prettierrc.json`，
  会按默认的行宽 80 把整个文件拆得稀碎。别换别的版本：不同版本排出来不一样，diff 全是噪音。
  手工对齐的数据表（像素格子、坐标表）前面加一行 `// prettier-ignore`。`.prettierignore` 里的不格式化：`components/ui/`、`ai-elements/`
  是拷进来的上游组件，`index.css` 末尾有原样搬的 xterm.css。

## 设计参考

对话界面的排版照这两个站做的，改对话里的东西、加新组件前先去看对应那一页，保持一个调子：

- **Astryx 的 Chat 组件**（https://astryx.atmeta.com/components/ChatMessage ，左边栏 Chat 下面每一个都可以参考）：
  用户气泡和下面那行时间（ChatMessage / ChatMessageMetadata → `packages/chat/src/components/MessageView.tsx`）、
  工具调用行和折叠组（ChatToolCalls → `packages/chat/src/components/ToolCall.tsx`、`packages/chat/src/components/Rows.tsx`）、
  居中的系统小字（ChatSystemMessage → MessageView 里的 data-notice）、输入框（ChatComposer → `packages/chat/src/components/Composer.tsx`）。
- **beUI Pro**（https://pro.beui.dev ，左边栏 AGENTS 那一组）：运行中的像素转圈（AI Thinking Loaders 的 Pixel orbit
  → `packages/chat/src/components/PixelLoader.tsx`）、提问卡片（Questionnaire → `packages/chat/src/components/Cards.tsx` 的 AskCard）。
  **它是付费组件库：只照着看效果自己写，不要拷它的代码。**
- **logo**：一只银灰色边境牧羊犬的狗头（扁平色块；herdr 是牧人、logo 是羊，gerdr 是帮他看住一群 agent 的狗）。
  `public/icon.svg` 是唯一的原稿，App 图标（`desktop/res/icon.png`）用仓库根上的 `make icon` 从它生成，改了要重跑。
  运行中的 loading 不用狗（16px 整条狗看不清、狗脸画不好看，试过），用上面 beUI 的像素转圈。
  备用素材在仓库的 `docs/brand/`：像素狗爪印 `paw.svg`、三个爪印一步步落下的动画 `paw-walk.svg`（都跟 currentColor 走），
  需要一个小尺寸的「狗」时先用它；「终端小怪」`critter.svg` / `critter-run.svg`（logo 落选的方向：长了眼睛和腿的终端窗口，
  标题栏三个点是蓝白橙，动起来是三个点轮流亮 + 眨眼），可以当吉祥物用在空状态、报错页之类的地方。

## 常见的改法

- **Add-on or UI edit?** A block in the sidebar or a pane's title bar, a button in the composer, a right-click item,
  a tag on the line under each turn, a command or key, a process that keeps running: write an **add-on** instead of
  editing this directory. It survives gerdr upgrades without merges, can be switched off, and can be shared through a
  store. How: `~/.gerdr/addons/GUIDE.md` (the add-on guide; the API is `~/.gerdr/addons/gerdr.d.ts`). Edit the UI
  source only for what no add-on slot reaches.

- **加一块显示**（比如每周 / 每月额度）：数据从哪来先看 `src/lib/api.ts`（已有接口）和 `src/lib/types.ts`；
  只有前端能算出来的直接在组件里算。放在哪：侧栏底部 `Chrome.tsx` 的 Sidebar、pane 标题栏 `Panes.tsx`、输入框下面（`src/lib/chatHost.tsx` 的 composerBar 插槽）。
- **加一个弹窗**：新建一个组件文件（照 `NameDialogs.tsx` 的写法，关掉用 `closeDialog()`），在 store 的 `Dialog` 类型里加一种 `kind`，
  再到 `Dialogs.tsx` 里挂上；用 `@/components/ui/dialog`。
- **加一个页面**：新建 `src/pages/<名字>.tsx`（default export 一个组件），用 `import { Link } from "react-router"` 链过去。
- **手机上的样子**：判断用 `usePhone()`（JS 里要分岔的）或 `max-md:` class（纯样式）。手机上没有 hover，
  只在悬停时露出来的按钮要加 `[@media(hover:none)]:opacity-100` 之类让它常显。
- **改对话里某种东西的样子**：消息 `packages/chat/src/components/MessageView.tsx`，工具调用 `…/ToolCall.tsx`，审批 / 提问卡片 `…/Cards.tsx`。
- **加快捷键**：`src/lib/keys.ts` 的 `MAC_KEYS`（Windows / Linux 上带 ⌘ 的会被去掉，要在 `PC_EXTRA` 里另给一个）+ `src/lib/actions.ts` 的 `runAction`。

后端没有的数据（比如要查 API 用量）界面拿不到：告诉用户需要后端加接口，别在前端硬凑。

## 类型检查（可选）

运行不需要，想在改完跑一下 TypeScript 检查的话：`pnpm install && npx tsc --noEmit`（第一次要装开发依赖）。
