# gerdr-ui

[Gerdr](https://github.com/gerdr-dev/gerdr) 的界面。**没有打包环节**：Gerdr 直接读这个目录的源码（每个文件用 esbuild 单独编译、
第三方包按 importMap 从 esm.sh 加载、Tailwind 在浏览器里生成），保存即生效，React Refresh 热替换。

```bash
git clone <这个仓库> my-ui
GERDR_UI=$PWD/my-ui gerdr          # 用这份界面启动 gerdr
```

不需要 `npm install` 就能跑；想做类型检查再 `pnpm install && npx tsc --noEmit`。

Gerdr 自带一份（第一次启动装到 `~/.gerdr/ui`），平时直接改那份就行 —— 或者让 Gerdr 里的 agent 改：
它们有内置的 `gerdr-ui` skill，知道怎么改、改完怎么确认没改坏。规矩和目录结构见 [AGENTS.md](AGENTS.md)。
