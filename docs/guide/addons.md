# Add-ons

<sub>[简体中文](addons.zh-CN.md)</sub>

Add-ons live in `~/.gerdr/addons/`, one folder each, live on save (no Node needed, Gerdr compiles them). Settings →
Add-ons lists them, switches them on and off, and has a **Store** tab: a store is a GitHub repository whose folders
holding an `addon.json` are add-ons, browsed offline, refreshed when you ask, installed with one click. The default
store is [gerdr-dev/gerdr-addon](https://github.com/gerdr-dev/gerdr-addon) (duplicate a tab with `prefix+a`, a
notification when a hidden session finishes, …); add any `owner/repo`.

```
~/.gerdr/addons/hello/
  addon.json   {"name": "Hello", "version": "1.0.0", "description": "…", "icon": "icon.png"}  (optional)
  index.ts     the code; may import other files of the folder
  icon.png     any files it ships: g.asset("icon.png") is their URL
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

`addon.json` can also name a **service**: a long-running process Gerdr's backend starts, restarts and stops with the
add-on. It gets the open sessions as JSON lines on stdin, and talks to the add-on's UI half (`g.service`) — e.g. a
bridge that keeps a Bluetooth wristband connected and shows agents on it.

How to write one — files, every API with examples, styling, settings, services, publishing to a store — is in the
[add-on guide](../../internal/addons/builtin/GUIDE.md) (also installed as `~/.gerdr/addons/GUIDE.md`); the exact API is in
`gerdr.d.ts` next to it. (They were called plugins
before; `~/.gerdr/plugins` moves to `~/.gerdr/addons` by itself.)
