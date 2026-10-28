---
name: gerdr-ui
description: "Change the gerdr app's own interface (the GUI you are running inside): add a panel, a dialog, a page, a quota display, restyle something, add a shortcut. gerdr's UI is plain source code read live with hot reload, so edits show up immediately. Use when the user asks to change/customize gerdr's UI or add something to it. Requires GERDR_ENV=1."
---

# 改 gerdr 的界面

gerdr（你正跑在里面的这个图形界面）的界面就是一个目录里的 React 源码，**没有打包环节**：
改完保存，用户眼前的界面就跟着变（热更新）。所以「界面上加一个 X」这种要求，直接改源码就行。

先确认在 gerdr 里：

```bash
test "${GERDR_ENV:-}" = 1
```

## 1. 找到界面目录、读规矩

```bash
gerdr ui status        # .dir 是界面源码目录
```

然后**先读那个目录里的 `AGENTS.md`**：目录结构、能 import 什么、样式怎么写、常见改法都在里面。不读就动手，
最常见的错是 import 了 package.json 里没写的包、或者在代码里 import CSS —— 这两样都会让整个界面编译失败。

## 2. 改之前存档

界面目录是一个 git 仓库（gerdr 用它把新版界面和你们的改动三方合并：`upstream` 分支是 gerdr 发的版本，`main` 是正在用的）。
改之前提交一次，改坏了能整个回去：

```bash
cd "<界面目录>" && git add -A && git commit -qm "改界面之前" || true
```

**只在 `main` 上改，别动 `upstream` 分支、别 rebase / 改历史** —— gerdr 升级时靠它算三方合并。

如果 `gerdr ui status` 的 `git.pending_upstream` 为 true：gerdr 有新版界面因为和本地改动冲突没合进来。用户要的话：

```bash
gerdr ui merge          # 冲突留在文件里（<<<<<<< 标记）；这时界面会编译失败，尽快解决
# 逐个解决冲突（两边的意图都保留），然后：
cd "<界面目录>" && git add -A && git commit -qm "合入新版界面，解决冲突"
gerdr ui status         # 确认没改坏
```

## 3. 改

- 小步改，一次改一处，保存后马上验；
- 照着周围代码的写法：组件放 `src/components/`，基础组件用 `src/components/ui/` 里现成的（shadcn），图标用 `lucide-react`；
- 颜色用主题 class（`bg-background`、`text-muted-foreground`、`bg-brand`……），深浅色都要能看；
- 只动用户要的那块，别顺手重构别的。

## 4. 验

每次保存后：

```bash
gerdr ui status
```

- `build_error` 非空：编译失败，用户界面上现在盖着一层红色报错 —— 立刻按里面的文件和行列修好；
- `runtime_errors`：页面里冒出来的错误（渲染出错、点了按钮抛异常）。只有页面开着、走到那段代码时才有，
  所以加了交互的话请用户点一下试试，再查一次；
- 都空就是没改坏。

改完提交一次（`git add -A && git commit -m "<改了什么>"`），告诉用户改了什么、在界面哪里能看到。
改坏了又修不好，就 `git checkout -- .` 回到存档，跟用户说明。

主界面坏到打不开时，用户还有一个内置的急救面板（`/rescue`，编进 gerdr、不在界面目录里，改不到）：
在那里能看错误、退回之前的一版、开一个在界面目录里干活的对话叫 agent 修。你可能就是从那里被叫来的。

## 做不到的

界面只能显示后端给得出的数据（接口都在 `src/lib/api.ts`）。后端没有的数据（比如 API 用量）前端拿不到，
别硬编数字 —— 告诉用户这需要后端加接口。
