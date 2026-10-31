# Gerdr add-ons

Each folder here is an add-on; saving a file takes effect at once.

```
my-addon/
  addon.json   optional: {"name": "…", "version": "1.0.0", "description": "…", "icon": "icon.png"}
  index.ts     export default defineAddon({ setup(g) { … } })
  icon.png     any other files it needs: g.asset("icon.png") is their URL
```

- Settings → Add-ons lists them (with errors, if one fails to load) and switches them on and off; its Store tab
  installs more. A folder whose name starts with `_` isn't loaded.
- `addon.json` can also declare a service: a process Gerdr's backend keeps running (holding a device like a
  wristband, anything that must run once and all the time). How it talks to Gerdr: the top of `gerdr.d.ts`.
- How to write one: `GUIDE.md`. The exact API: `gerdr.d.ts`. Gerdr refreshes both on every start: don't edit them.
- A broken add-on doesn't take the UI down: its error shows in Settings → Add-ons.

The smallest add-on, `hello/index.ts`:

```ts
/// <reference path="../gerdr.d.ts" />
export default defineAddon({
  setup(g) {
    return g.paneTitle((pane, { label }) => `🤖 ${label}`);
  },
});
```
