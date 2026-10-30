# 对话列表的滚动：闪、跳、停不到底，怎么复现、怎么查

对话是虚拟列表（react-virtuoso，`ui/src/components/chat/ChatView.tsx`）：只挂屏幕附近的行，滚出去的卸掉，
卸掉的行用记下的高度垫着占位。所以**任何一行「记的高度」和「实际高度」对不上，滚到那儿就会跳**；
任何一次「布局变了但 scrollTop 没跟上」，眼前的内容就会被推走。这类问题肉眼只看得到「闪了一下」，
靠猜几乎改不对（4caa31c 之前在补偿逻辑上打过好几轮补丁，都是在别处跳得更厉害）。办法只有一个：**逐帧量**。

`tools/scrolltest/` 是这套量法的脚本。改虚拟列表、对话里会变高矮的东西（Markdown、代码块、图片、折叠组）、
滚动 / 贴底 / 分段加载之后，跑一遍对应的场景。

## 怎么跑

```bash
cd tools/scrolltest && npm install          # 只装 playwright-core，用系统的 Chrome（和 Electron 一样是 Chromium）
gerdr session list                           # 找出闪的那个会话的 id
node setup.mjs <会话 id> [<会话 id>…]         # 复制进 ~/.gerdr-dev，一个会话一个 tab（调试实例要先停着）
make dev                                     # 回仓库根起调试实例（7871，界面直接用仓库的 ui/，改了热更新）
node tabs.mjs                                # 然后跑场景，见下表
```

脚本只对着调试实例（`~/.gerdr-dev`、7871）跑，指到正在用的 7870 会拒绝：它们会点 tab、改 keepTabs，会把用户的布局点乱。
换实例用 `SCROLLTEST_PORT` / `SCROLLTEST_DIR`（**不读 `GERDR_PORT` / `GERDR_DIR`**：在 gerdr 里跑的 agent 环境里
这两个指着正在用的那个）。每个脚本有问题时退出码是 1。

| 场景 | 脚本 | 查什么 |
|---|---|---|
| 打开会话 / 切回来那一下抖 | `open.mjs [--tab=1] [--reload]` | 重新挂载（keepTabs=1）或整页加载时逐帧看跳没跳、最后在不在底 |
| 切 tab 回来不在最底下 | `tabs.mjs [--keep=1]` | 来回切，16 / 150 / 600 / 1500ms 各量一次离底部多远 |
| 滚着滚着一闪 | `fling.mjs [--tab=1] [--from="某段话"]` | 触控板式甩动（一串递减的滚轮），逐帧看内容有没有不跟着滚动走 |
| 翻到顶补一段时跳 | `fling.mjs --to-top` | 一直往上甩到头，分段加载每补一段都看一遍 |
| agent 输出时内容在动 / 不跟底 | `stream.mjs --at="用户说的话" [--up=600\|0]` | 截到那句话，后面那一轮流式灌进去；往上翻着看内容动没动 / 贴着底看跟没跟 |
| Phone: switching panes shows a mid-chat frame, then snaps to the bottom | `panes.mjs [--stream] [--cpu=4] [--ro]` | One tab split in two, phone viewport, focus flipped back and forth (only the focused pane is drawn, so each switch remounts). `--cpu` slows the CPU like a phone, `--stream` feeds output in while switching, `--ro` judges the painted frames |
| 某一行自己变高矮 | `rows.mjs [--tab=1]` | 行挂上后高度变没变（DOM 没变也会变）、Virtuoso 记的高度和实际对不对 |

用户说「就在这儿闪」：拿那个会话，`fling.mjs --from="那段话"`，先翻到那儿再甩。闪不闪跟内容关系很大，别拿别的会话代替。

## 量的是什么（`lib.mjs` 的 analyze）

每个 `requestAnimationFrame` 记一帧：scrollTop、总高、列表是否还 `visibility: hidden`、屏幕上每一行的 top 和高度。
相邻两帧都在屏幕上的同一行，屏幕上挪了 `vis`、scrollTop 变了 `ds`：

- 人没在滚：`vis ≠ 0` 就是跳（贴着底、正好跟着 scrollTop 走是在跟新输出，不算）。
- 人在滚：正常 `vis = -ds`。`vis + ds ≠ 0` 说明这一帧布局也变了；`vis ≈ 0` 是有人补过了（compensated），
  否则是跳。补的那一帧人还在甩，剩一点和滚动同方向、不超过一帧甩动量的 `vis` 也算补过了。
- 两帧没有一行相同：整屏换了内容（swapped）。

**认行用 `data-item-index`，别用 `data-index`。** `data-index` 是在当前数据里的位置，往前补一段就全变了；
`data-item-index` = firstItemIndex + 位置，补的时候同一行不变。界面代码里也一样（下面「往前补一段」）。

列表还 hidden 的帧不算：Virtuoso 定好初始位置之前把列表藏着，这期间跳多少都看不见。

## 哪一帧画出来了

rAF 里看到的不一定是画出来的。一帧的顺序是：rAF 回调 → ResizeObserver 回调 → 画。
Virtuoso 在 ResizeObserver 里量行高、改布局，所以 rAF 里看到「对的」，画出来可能是错的；反过来 rAF 里看到错位，
如果后面 ResizeObserver 里有人补了，画出来又是对的。

要确认：`startFrameLog(page, { ro: true })` 另挂一个 ResizeObserver 记一份（`window.__roFrames`）。它比 Virtuoso 的晚建，
同一阶段按创建顺序排在后面跑，记到的就是这一帧要画的样子。录屏（`launch({ video })`）也行，但 headless 录屏帧率低、
压缩，几毫秒的一帧常常录不到，只适合看「空白了几帧」这种。

修的时候同理：**要赶在画之前补，就在 ResizeObserver 里补，而且这个 observer 要比 Virtuoso 的晚建**
（这时 Virtuoso 该补的已经补完，只补它漏的）。放在 rAF 里补，一是晚一帧、错位先画出来了，二是抢在 Virtuoso 前面，
它本来会补的被补两遍。

## 踩过的坑（都是逐帧量出来的）

- **代码块的 `content-visibility: auto`**（streamdown 写的行内样式，占位 200px）：屏幕外的块浏览器不排版、按 200px 算，
  滚近了才排成真实高度。虚拟列表提前挂上的行被量成占位高度 → 往上翻一闪一闪。同一行 899 → 753px，DOM 一个字节没变。
  `index.css` 里用 `!important` 压成 visible。再出现「DOM 没变、高度变了」先查这类：`content-visibility`、懒加载图片、字体。
- **Videos in the chat (`<video>`, `chat/ChatMedia.tsx`)**: until metadata arrives a video is the browser default
  300x150, then it grows to its real size (320px tall here). Every remount in the virtual list starts over, so each row
  with a video swung by ~170px (a row with five videos by 840px) and the list jumped whenever it scrolled past. The box
  is now reserved from the aspect ratio (remembered per URL, 16:9 until known). Second trap found on the way: the
  wrapper was `w-fit` and the video's width a percentage of it, so the wrapper took the video's 300px default width and
  the reserved box came out half size. Anything sized as a percentage of a `w-fit` parent has this loop.
- **Bottom-follow lagging behind a row that grew** (phone pane switch, `panes.mjs`): the user's screen recording showed,
  after each switch, one or two frames of content from a few screens up, then a snap to the bottom. Following the
  bottom relied on `totalListHeightChanged`, which Virtuoso only calls after it has measured and reported the new total:
  one to several frames after the row actually grew (a remounted row going from its estimate to its real height, the
  last row growing under a streaming reply). Those frames were painted short of the bottom. ChatView now also sticks
  to the bottom from a ResizeObserver on the item list created after Virtuoso's (in an effect), so it runs before paint
  with the same conditions. Headless Chrome on a laptop only shows this with `--cpu=4` and `--ro`: in rAF frames the lag
  shows up with or without the fix, in the painted frames only without it.
- **Virtuoso 把行高取整**（默认 `Math.round(getBoundingClientRect().height)`）：41.5 记 42、39.8 记 40，
  卸掉的行越多攒得越多，量完缩回那一下整段挪一截（卸 32 行差 10px），会话越长越明显。`itemSize` 换成不取整的。
  `rows.mjs` 报「记的和实际不一样」就是这个。
- **往前补一段（firstItemIndex 往小改）**：Virtuoso 只在插进去那一下按估的行高 scrollBy 一次，新行量出真实高度
  （差几千像素）后不再补 → 整屏被顶走。ChatView 里补完 1.5 秒内盯住眼前顶上那一行，在 ResizeObserver 里补差额。
- **「在底部」被布局变化改成 false**：重新挂载、边挂边量时 scrollTop 被临时推离底部一截，onScroll 就记成不在底部，
  之后不再追底、还记进 scrollMem，下次切回来照着恢复到半中间（tab 不保留时切面板必现）。
  现在离开底部只认人在这个滚动区里的输入（滚轮 / 触摸 / 按键 / 按住拖），没人动就推回底部。
  输入只认滚动区里的：切 tab 本身就是一次点击，全局算的话正好把挂载那段放过去了。
- **测试环境和真机不一样的地方**：headless 默认藏滚动条（mac 接鼠标时滚动条常显、占宽度，宽度不同行高就不同）；
  dpr 要 2；要拿出事的那个会话；「刚打开就滚」和「开着跑了好几轮再滚」不一样（后者有行在屏幕外变过内容）——
  后者用 `stream.mjs` 先灌再翻。
- 显式传 `itemSize={undefined}` 会让 Virtuoso 报 `e is not a function`，要么传函数要么不传这个 prop。

## 加一个场景

照现有的脚本写：`launch` → `open` → `useStore`（拿到页面里的 store，能改 config、`applyEvents` 灌事件）→
`tab(n)` → `startFrameLog` → 动手 → `stopFrameLog` → `analyze` / `report`。验证脚本有没有用：
把要防的那处修复临时改掉，脚本应该报出来、退出码 1；改回来应该过。
