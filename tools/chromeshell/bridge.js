// Injected into every page of ours before its own scripts (Page.addScriptToEvaluateOnNewDocument): the same
// window.gerdrNative desktop/preload.js gives the UI (ui/src/lib/native.ts reads it), carried over a CDP binding
// (window.__gerdrPost, Runtime.addBinding) instead of Electron IPC.
(() => {
  if (window.gerdrNative) return;
  // Looked up per call: in a fresh document the binding can be installed after this script runs.
  const post = (m) => window.__gerdrPost(JSON.stringify(m));

  let seq = 0;
  const pending = new Map();
  const parked = new Map();
  window.__gerdrFiles = parked;
  window.__gerdrResolve = (id, v) => {
    const r = pending.get(id);
    if (!r) return;
    pending.delete(id);
    parked.delete(id);
    r(v);
  };
  const ask = (type, file) =>
    new Promise((resolve) => {
      const id = ++seq;
      pending.set(id, resolve);
      if (file) parked.set(id, file);
      post({ type, id });
    });

  // Electron's webUtils.getPathForFile is synchronous, and so are its callers (ChatHost.localPath in @gerdr/chat).
  // Over CDP the shell can only answer asynchronously (DOM.getFileInfo on the File), so the paths are looked up
  // *before* the page sees the files: a dropped / picked / pasted file event is held in the capture phase, every
  // File's path fetched, and the event dispatched again with the very same File objects. A few ms, once per drop.
  const paths = new WeakMap();
  const resolveAll = (files) =>
    Promise.all(files.map((f) => (paths.has(f) ? null : ask("__filePath", f).then((p) => paths.set(f, p || "")))));
  const replaying = new WeakSet();
  const hold = (e, files, again) => {
    if (!e.isTrusted || replaying.has(e) || !files.length || files.every((f) => paths.has(f))) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    const target = e.target;
    resolveAll(files).then(() => {
      const ev = again();
      replaying.add(ev);
      target.dispatchEvent(ev);
    });
  };
  const transfer = (src, files) => {
    const dt = new DataTransfer();
    for (const t of src.types) if (t !== "Files") dt.setData(t, src.getData(t));
    for (const f of files) dt.items.add(f);
    return dt;
  };
  addEventListener(
    "drop",
    (e) => {
      const files = Array.from(e.dataTransfer?.files ?? []);
      const dt = files.length ? transfer(e.dataTransfer, files) : null;
      const init = { bubbles: true, cancelable: true, composed: true, clientX: e.clientX, clientY: e.clientY, screenX: e.screenX, screenY: e.screenY, altKey: e.altKey, ctrlKey: e.ctrlKey, metaKey: e.metaKey, shiftKey: e.shiftKey };
      hold(e, files, () => new DragEvent("drop", { ...init, dataTransfer: dt }));
    },
    true,
  );
  addEventListener(
    "paste",
    (e) => {
      const files = Array.from(e.clipboardData?.files ?? []);
      const dt = files.length ? transfer(e.clipboardData, files) : null;
      hold(e, files, () => new ClipboardEvent("paste", { bubbles: true, cancelable: true, composed: true, clipboardData: dt }));
    },
    true,
  );
  addEventListener(
    "change",
    (e) => {
      const el = e.target;
      if (!(el instanceof HTMLInputElement) || el.type !== "file") return;
      hold(e, Array.from(el.files ?? []), () => new Event("change", { bubbles: true }));
    },
    true,
  );

  // Links leaving Gerdr go to the default browser (Electron: setWindowOpenHandler / will-navigate). Without this they
  // would open in our own Chrome, whose browser windows have no Dock icon and no way back.
  const external = (href) => {
    try {
      const u = new URL(href, location.href);
      return /^(https?|mailto):$/.test(u.protocol) && u.origin !== location.origin ? u.href : "";
    } catch {
      return "";
    }
  };
  const open = window.open;
  window.open = function (u, ...rest) {
    const x = u && external(String(u));
    if (x) {
      post({ type: "open", url: x });
      return null;
    }
    return open.call(this, u, ...rest);
  };
  addEventListener(
    "click",
    (e) => {
      if (e.defaultPrevented || e.button > 1) return;
      const a = e.composedPath().find((n) => n instanceof HTMLAnchorElement && n.href);
      const x = a && external(a.href);
      if (!x) return;
      e.preventDefault();
      post({ type: "open", url: x });
    },
    false, // bubble, on window: after the UI's own handlers (one that opened it itself has called preventDefault)
  );

  // The app's manifest (the shell answers it, see serveManifest): a gerdr from before it linked one still installs.
  const linkManifest = () => {
    if (document.querySelector('link[rel="manifest"]')) return;
    const l = Object.assign(document.createElement("link"), { rel: "manifest", href: "/manifest.webmanifest" });
    document.head.append(l);
  };
  if (document.head) linkManifest();
  else document.addEventListener("DOMContentLoaded", linkManifest, { once: true });

  // Right-click: no browser menu (Back, Reload, Save As, Print, Inspect) where the UI didn't answer with its own.
  // Electron shows none at all; the browser's stays only where it is useful: an editable field or selected text
  // (cut / copy / paste). On window, bubble phase: after the UI's own handlers (theirs call preventDefault).
  addEventListener("contextmenu", (e) => {
    if (e.defaultPrevented) return;
    const t = e.target instanceof Element ? e.target : null;
    const editable = t && t.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"], [contenteditable="plaintext-only"]');
    if (!editable && !String(getSelection() || "")) e.preventDefault();
  });

  window.gerdrNative = {
    postMessage: (m) => post(m),
    getPathForFile: (f) => paths.get(f) || "",
    platform: navigator.platform.startsWith("Mac") ? "darwin" : navigator.platform.startsWith("Win") ? "win32" : "linux",
    shell: "chrome",
  };
})();
