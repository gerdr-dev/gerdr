# gerdr add-ons

Each folder here is an add-on; saving a file takes effect at once.

```
my-addon/
  addon.json   optional: {"name": "…", "version": "1.0.0", "description": "…", "icon": "icon.png"}
  index.ts     export default defineAddon({ setup(g) { … } })
  icon.png     any other files it needs: g.asset("icon.png") is their URL
```

- Settings → Add-ons lists them (with errors, if one fails to load) and switches them on and off; its Store tab
  installs more. A folder whose name starts with `_` isn't loaded.
- `addon.json` can also declare a service: a process gerdr's backend keeps running (holding a device like a
  wristband, anything that must run once and all the time). How it talks to gerdr: the top of `gerdr.d.ts`.
- The API is in `gerdr.d.ts` (gerdr refreshes it on every start: don't edit it).
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
