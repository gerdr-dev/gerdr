# 对话列表的滚动：闪、跳、停不到底，怎么复现、怎么查

对话是虚拟列表（our own `ui/packages/chat/src/components/ChatList.tsx`; it was react-virtuoso until fb9189f, the
old code is in git history）：只挂屏幕附近的行，滚出去的卸掉，
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

The scripts still find the old react-virtuoso list too (`[data-virtuoso-scroller], [data-chat-scroller]`), for
checking out an older commit. If the debug build doesn't compile (someone else's work in
progress), start the installed binary instead: `GERDR_UI=$PWD/ui GERDR_DIR=~/.gerdr-dev GERDR_PORT=7871 gerdr`.
Run `panes.mjs` / `tabs.mjs` / `open.mjs` last or `setup.mjs` again after them: they leave the debug layout split or
on another tab, and the scripts after them find no chat.

脚本只对着调试实例（`~/.gerdr-dev`、7871）跑，指到正在用的 7870 会拒绝：它们会点 tab、改 keepTabs，会把用户的布局点乱。
换实例用 `SCROLLTEST_PORT` / `SCROLLTEST_DIR`（**不读 `GERDR_PORT` / `GERDR_DIR`**：在 Gerdr 里跑的 agent 环境里
这两个指着正在用的那个）。每个脚本有问题时退出码是 1。

| 场景 | 脚本 | 查什么 |
|---|---|---|
| 打开会话 / 切回来那一下抖 | `open.mjs [--tab=1] [--reload]` | 重新挂载（keepTabs=1）或整页加载时逐帧看跳没跳、最后在不在底 |
| 切 tab 回来不在最底下 | `tabs.mjs [--keep=1]` | 来回切，16 / 150 / 600 / 1500ms 各量一次离底部多远 |
| 滚着滚着一闪 | `fling.mjs [--tab=1] [--from="某段话"]` | 触控板式甩动（一串递减的滚轮），逐帧看内容有没有不跟着滚动走 |
| 翻到顶补一段时跳 | `fling.mjs --to-top` | 一直往上甩到头，分段加载每补一段都看一遍 |
| agent 输出时内容在动 / 不跟底 | `stream.mjs --at="用户说的话" [--up=600\|0]` | 截到那句话，后面那一轮流式灌进去；往上翻着看内容动没动 / 贴着底看跟没跟 |
| Phone: switching panes shows a mid-chat frame, then snaps to the bottom | `panes.mjs [--stream] [--cpu=4] [--ro]` | One tab split in two, phone viewport, focus flipped back and forth (only the focused pane is drawn, so each switch remounts). `--cpu` slows the CPU like a phone, `--stream` feeds output in while switching, `--ro` judges the painted frames |
| Can't scroll up from the bottom, gets pulled back | `scrollup.mjs [--step=40] [--steps=25]` | Small wheel steps up from the bottom must stick. Run it after touching anything that keeps the chat at the bottom: the first 80px up still count as at the bottom, and the virtual list resizes on every step |
| Scrolling up while a reply streams in gets pulled back | `streamup.mjs [--step=30] [--steps=5] [--gap=1500]` | Small wheel steps up from the bottom with pauses longer than 600ms while output keeps arriving. Leaving the bottom goes by direction, not distance: any user scroll up leaves it, only scrolling back down (or reaching the very bottom) re-sticks (`syncBottom`) |
| Phone: a finger drags up a little mid-reply and stays down; the text under it bobs or runs on | `touchhold.mjs [--up=40] [--hold=3000] [--creep] [--height=462] [--cpu=4] [--ro]` | Touch drag (CDP touch events, phone viewport) from the bottom while a reply streams in, then holds still: no row may move. `--creep`: the finger keeps inching up the chat while held (the content may only follow it); `--height=462` is a phone with the keyboard up. `--up` under ~15px is eaten by the touch slop (no scroll, exit 2) |
| Phone: reading a reply as it's written, dragging / flicking now and then; whole screens jump | `touchscroll.mjs [--at="what the user said"] [--fling] [--cpu=4]` | Plays a turn back like `/replay` while dragging, checks every frame, and fails on a whole-chat remount mid-reply (laptop Chrome measures fast enough that no frame shows it; a phone jumps). Use `--at` the turn the user saw it in, steers included |
| The jump-to-bottom button stutters on the way down | `tobottom.mjs [--mobile] [--cpu=4] [--screens=20]` | Scrolls ~20 screens up (wheel, or a touch gesture with `--mobile`), presses the button and checks every frame: only ever moving down, no frame standing still or moving over 400px after the first, none over 50ms, ends at the bottom. rAF frames: a scroll resizes nothing, so `--ro` would see none |
| Phone: typing at the bottom of a chat; the chat jumps a line on each key | `keyboard.mjs [--type] [--cpu=4]` | The chat changes height while a reply plays (the keyboard / IME candidate bar coming and going): every painted frame must stay at the bottom. `--type`: types on the composer's second line and fails on any scroll event that leaves the bottom on its own (the composer's measuring clamped the chat, see below) |
| A broken image / video makes the chat jump | `media.mjs [--src=/tmp/x.png] [--tab=1]` | Appends a message with missing media, parks it mid-screen, watches through every automatic retry, then scrolls it out and back in. Run it after touching `chat/ChatMedia.tsx` |
| 某一行自己变高矮 | `rows.mjs [--tab=1]` | 行挂上后高度变没变（DOM 没变也会变）、Virtuoso 记的高度和实际对不对 |

用户说「就在这儿闪」：拿那个会话，`fling.mjs --from="那段话"`，先翻到那儿再甩。闪不闪跟内容关系很大，别拿别的会话代替。

When the phone shows what the scripts don't, measure on the phone: put a trace into ChatView (every scroll event,
every `scrollTop` / `scrollTo` / `scrollBy` with a short stack, input direction, at-bottom changes), collect it during
`/replay` and POST it to `/api/ui/errors` under a `build` that isn't the current one: the server logs it to
`~/.gerdr/logs/gerdr.log` and the rescue panel never shows it. That is how Virtuoso's compensation below was caught;
neither cause showed in headless Chrome (an 860px viewport, no keyboard).

To try it by hand (on a phone too, against the Gerdr you use): type `/replay` in a chat. It plays the latest turn with
some length back in that window only, about 150 characters a second, then puts the log back (`store.replayLastTurn`;
no agent, nothing sent).

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
  with the same conditions, plus two: the content height must actually have changed (the item list also resizes when
  rows mount / unmount while scrolling; reacting to that pulled every small scroll up back to the bottom, caught by
  `scrollup.mjs`), and no user input on the scroller in the last 600ms. Headless Chrome on a laptop only shows this with `--cpu=4` and `--ro`: in rAF frames the lag
  shows up with or without the fix, in the painted frames only without it.
- **Broken media retrying in place** (`chat/ChatMedia.tsx`, `media.mjs`): a failed image was remounted for each
  automatic retry (1s, 3s, 8s), so its row went broken-image -> reloading -> failed-chip, each a different height, and the
  virtual list restarted the whole cycle whenever the row scrolled back into view. Now the first failure switches to a
  fixed-height chip (spinner while retrying, "can't open" after), retries probe in the background (`fileStat` for
  local files) and only put the media back once the file is there, and the failure state is kept per URL across
  remounts.
- **A short scroll up mid-reply pulled back to the bottom** (`touchhold.mjs`): two causes, both needed fixing.
  (1) Leaving the bottom was read from the scroll event, which lands a frame after the input; a push to the bottom from
  new output in between undid the move, or landed on the very bottom afterwards and re-stuck it. Now the input itself
  decides (wheel up, finger dragging down, ↑ / PageUp / Home: `trackScrollDir`), and for 600ms after a move up landing
  on the bottom doesn't re-stick. (2) Virtuoso's `followOutput` as a function is only asked for a new row: when the
  list merely grows while Virtuoso counts itself at the bottom (its 80px threshold) it scrolls down for any value but
  `false` (`autoscrollToBottom`, "SIZE_INCREASED"). So off the bottom ChatView passes `false`, not a function returning
  false. Long drags (> 80px) never showed it, which is why "a long drag holds, a short one bobs".
- **A steer waiting at the bottom remounted the whole chat on every tool call** (`touchscroll.mjs --at=…`, from a phone
  recording where whole screens jumped while reading). A queued steer's bubble and the working row are the last two
  rows, so each new tool call changed the third row from the end, which the "rows changed in the middle, measure again"
  rule (`setMeasured(false)`, every row mounted and measured) took for a structural change. Now only a change above the
  first mounted row counts: from there down rows are measured as they are, and a change below the screen moves nothing
  in view. Strictly above: with `>=`, older messages put in front while scrolled to the top (row 0 changes, row 0 is the
  first mounted) skipped the measuring and `fling.mjs --to-top` jumped.
- **Virtuoso's "upward scrolling compensation" is off; ChatView keeps the top row in place itself** (trace from the
  user's phone, keyboard up so the chat was ~290px tall). While the last scroll went up, Virtuoso `scrollBy`s every
  change of its idea of the total height, as if rows above the screen changed. Two ways that was wrong: a reply being
  written grows at its bottom, below the top of the screen (taller than the screen, so its top was above it), so the
  text ran forward a line (+25.5px) every ~150ms while the user dragged up; and its per-index sizes went stale as rows
  shifted (a steer landing, tool rows coming and going): `scrollBy -225` four times in a second while the real height
  grew 40px each, throwing the user back to their own message. `keepBottomOnShrink` now drops Virtuoso's `scrollBy`
  except while older messages go in front (`settle` works with that one), and `keepAnchor` does the job: the row at the
  top of the screen, by key (`data-chat-row`), keeps its place on screen when its place in the content moves (rows
  above really changed). Run from the list's ResizeObserver, the MutationObserver on its style (top padding) and
  onScroll; the user's own scrolling moves no row within the content, so it isn't undone. A first try that tallied
  height changes below the top and swallowed matching compensations missed both: the compensation comes up to a few
  frames after the resize, and the stale-size ones match nothing. Switching compensation off without `keepAnchor`
  showed in `touchscroll.mjs`: rows above that really change while a reply streams (-229 / +501px) jumped.
- **The composer measuring itself clamped the chat a line short of the bottom.** `fitHeight` set the textarea to
  `height: auto` and read `scrollHeight`: in that forced layout the box is one line tall, the chat above a few lines
  taller, and the browser clamps the chat's scroll position to the new bottom; it stays there when the box grows back
  (measured: 196px off the bottom). Only from the second line on (on the first, "auto" is one line anyway); the chat's
  size is the same before and after, so no ResizeObserver hears of it. The box around the textarea is now held at its
  height while it measures. Rows in rAF frames lie here too: judge painted frames (`--ro`) or the scroll events.
- **TanStack Virtual, tried in Labs (`TanList.tsx`, "新的对话列表"), then set aside for a list of our own.** What it
  needed on top of its chat mode (`anchorTo: "end"`), each found by these scripts or on the user's phone:
  following the bottom ourselves (its `followOnAppend` follows appends only: a reply growing in place, rows measured
  after opening and the log being replaced all left it short of the bottom, up to 17000px); "at the bottom" = 2px,
  not 80 (else the distance from the end is kept while output grows, and text runs under a finger 40px up); leaving it
  by input direction (as above); every row measured once before shrinking to the rows near the screen (else 6-7
  jumps scrolling up, rows coming in at the estimate); hidden until positioned (the first render shows rows 0-2);
  sizes of rows on screen and below handed over by our own ResizeObserver before paint (its own hands them over a
  frame or more later: rows drawn over each other, the bottom lagging; on a phone ~0.5s), but rows wholly above the
  screen left to it (sizing those ahead of it broke its prepend anchoring: 2-4 jumps); rows absolutely placed (in
  normal flow a row growing above the screen moves what's on screen before it compensates); following the chat's
  own size changes (keyboard). Still wrong when set aside, on the user's phone only: a very long chat opened partway
  up instead of at the bottom (not reproduced headless, CPU x6, reload or reopening from the list), and the
  jump-to-bottom button scrolling in stutters. Lesson for our own list: one place that owns scrollTop, sizes known
  before paint, the bottom followed by us, positions by row key.
- **Our own list (`ChatList.tsx`), the chat list since; react-virtuoso and its patches in `ChatView.tsx` removed.** The
  Virtuoso entries above describe that removed code; they stay as the record of what a chat list has to handle. Built on that lesson: one function (`sync`) measures rows,
  lays them out from sizes kept by key, writes positions to the DOM and puts scrollTop where it belongs, all before
  paint (after every commit and from a ResizeObserver on the rows). At the bottom: the end. Otherwise the row at the
  top of the screen keeps its distance from the screen's top, found by key in the layout from before the change; that
  one rule covers rows above changing size, older messages going in front, and a reply growing under the top row.
  What the scripts caught while building it:
  - The jump-to-bottom button: Virtuoso's smooth scroll (the default list) jumps up to 2000px in a frame and drops
    frames on a phone profile (`tobottom.mjs --mobile --cpu=4`: 10 steps over 400px, 3 frames over 50ms), the stutter
    the user saw. Ours animates itself, towards a target recomputed each frame, after skipping to 1.5 screens short.
  - A finger's touchmoves stop reaching the list when the row under it is replaced (a tool call's "peek" row becomes
    its group: a new key, the touched node detached). The scroll still happens but the list never heard the finger
    move up, stayed stuck and pulled back to the bottom on every line (`touchhold.mjs --height=462`, only when the
    finger lands on such a row). So a scroll up while a finger is down (from touchstart) leaves the bottom too. The
    Virtuoso path's `trackScrollDir` reads touchmove the same way and has the same hole.
  - Some rows finish rendering a frame after they mount (one grew 200px): hidden for two frames on opening, as Virtuoso
    does (`open.mjs --reload`). The cause turned out to be Tailwind running in the browser, compiling classes it hasn't
    seen a frame late (docs/dev/TAILWIND.md). So only the first chat opened in a page is hidden; hiding every open
    showed a blank chat for two frames on each session switch on a phone (`panes.mjs --cpu=4` reports `hidden 0` now).
    A session using classes no chat before it used can still resize once on its first open.
  - Frames from the previous tab's chat counted as a "whole screen swapped" when switching tabs: the frame log now
    tags each frame with its scroller and only compares frames of the same one.
  - Older messages coming in: the rows of a message cut off at the top get new keys once the rest of it arrives, so
    the anchor row's key can vanish; then the next row from before that still exists anchors (`fling.mjs --to-top`:
    the whole screen swapped on the first load). The new rows are mounted once beside the range, measured and dropped,
    instead of mounting the whole chat again (`touchscroll.mjs --fling --height=462`). The remount check there skips
    frames that mount rows with a lower item index than any seen before: those are older messages, not a remount.
  - A width change (sidebar toggled, seam dragged, a split pane zoomed) used to measure every row again the way
    opening does: the whole chat mounted in one flushSync, ~600ms headless per change on a 15MB session with older
    pages loaded, ~700ms in the user's profile per pane zoom. Now only the mounted rows are measured; the rest keep
    their old-width size as an estimate until they mount, and the top row's anchor absorbs the difference
    (`fling.mjs --resize [--to-top]`: the same jumps as before the change, all rows resizing on their own).
  - A tab not kept mounted (keepTabs 1) remounts its chat on every switch, and opening measured every loaded row by
    mounting them all: 768ms switching back to a 15MB session with older pages loaded (headless). Measured sizes now
    outlive the list in a module-level cache by pane + session (`memKey`, the scroll memory's key), used when the width
    is the same: only the rows near where it opens are mounted (156ms). Stale cached sizes are measured as their rows
    mount, like after a width change. `tabs.mjs --keep=1`, `open.mjs --reload`, `scrollup.mjs`: no jumps; a position
    mid-chat comes back to the same row and offset.
  - `keyboard.mjs --type` judged rAF frames, which run before layout and show the composer's new height before the
    list has stuck to the bottom. The `--ro` log now also watches the scroller, so it sees frames where only the chat's
    own size changed, and both modes judge painted frames.
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
