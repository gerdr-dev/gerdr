# gerdr 插件目录

这里的每个 `.ts` 文件（或带 `index.ts` 的子目录）都是一个插件，保存就生效。

- 文件名以 `_` 开头的不加载（`_notify-when-done.ts` 就是这样默认关着的）。
- API 见 `gerdr.d.ts`（gerdr 每次启动会刷新它，别改）。
- 写错了不会让界面挂掉：错误会显示在「设置 → 插件」里。

自带三个：

| 文件 | 干什么 |
|---|---|
| `tab-numbering.ts` | tab 名前加位置编号，`1.claude`、`2.web`，同名的加罗马数字后缀 |
| `duplicate-tab.ts` | `prefix+a` / `prefix+i` 复制当前 tab（插到后面 / 前面） |
| `_notify-when-done.ts` | 示例：看不见的会话跑完了 / 在等你时弹系统通知（默认关） |

最小的插件：

```ts
/// <reference path="./gerdr.d.ts" />
export default definePlugin({
  setup(g) {
    return g.paneTitle((pane, { label }) => `🤖 ${label}`);
  },
});
```
