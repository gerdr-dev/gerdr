# 扩展

<sub>[English](addons.md)</sub>

扩展放在 `~/.gerdr/addons/`，一个文件夹一个，保存即生效（不用装 node，Gerdr 自己编译）。「设置 → 扩展」里能看到装了哪些、
开关它们，还有一个**商店**页：商店就是一个 GitHub 仓库，里面每个带 `addon.json` 的文件夹是一个扩展；离线浏览、手动刷新、
点一下安装。默认商店是 [gerdr-dev/gerdr-addon](https://github.com/gerdr-dev/gerdr-addon)（`prefix+a` 复制 tab、
看不见的会话跑完了弹通知……），也能加任意 `owner/repo`。

```
~/.gerdr/addons/hello/
  addon.json   {"name": "Hello", "version": "1.0.0", "description": "…", "icon": "icon.png"}（可选）
  index.ts     代码，可以 import 同文件夹里的别的文件
  icon.png     随扩展带的文件：g.asset("icon.png") 就是它的地址
```

```ts
/// <reference path="../gerdr.d.ts" />
export default defineAddon({
  setup(g) {
    g.paneTitle((pane, { label }) => `🤖 ${label}`);
    g.command({ id: "hello", title: "Say hello", key: "prefix+y", run: () => g.notify("hello") });
  },
});
```

`addon.json` 还可以声明一个**后台进程**：Gerdr 后端负责起、挂了重启、扩展删了或关了就停掉。
它从 stdin 一行一个 JSON 收到开着的会话，也能和扩展的界面那一半（`g.service`）互发消息 —— 比如一直连着蓝牙手环、把 agent 状态推上去。

怎么写一个扩展（文件结构、每个 API 和例子、样式、设置项、后台进程、发到商店）见[扩展开发手册](../../internal/addons/builtin/GUIDE.md)
（也装在 `~/.gerdr/addons/GUIDE.md`）；准确的 API 在同目录的 `gerdr.d.ts`。（以前叫插件；原来的 `~/.gerdr/plugins` 会自动搬到 `~/.gerdr/addons`。）
