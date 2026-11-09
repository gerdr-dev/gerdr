// Injected into every page before its own scripts (WebView2 AddScriptToExecuteOnDocumentCreated): the
// window.gerdrNative desktop/preload.js gives the UI (ui/src/lib/native.ts reads it), carried over a WebView2 binding
// (window.__gerdrPost, main.go). Same rules as the macOS shell's (tools/chromeshell/bridge.js) where they apply.
(() => {
  if (window.gerdrNative) return;
  const post = (m) => window.__gerdrPost(JSON.stringify(m));

  // Notifications: the web API, shown by WebView2 as Windows notifications of this app. A click brings the window up
  // and shows the conversation, like the Electron shell's.
  const live = new Set(); // a collected Notification loses its click handler
  const notify = (m) => {
    const show = () => {
      const n = new Notification(m.title || "Gerdr", { body: m.body || "" });
      live.add(n);
      if (live.size > 50) live.delete(live.values().next().value);
      n.onclick = () => {
        live.delete(n);
        post({ type: "__focus" });
        if (m.sessionId && window.gerdr && window.gerdr.focusSession) window.gerdr.focusSession(String(m.sessionId));
      };
      n.onclose = () => live.delete(n);
    };
    if (Notification.permission === "granted") show();
    else if (Notification.permission !== "denied") Notification.requestPermission().then((p) => p === "granted" && show());
  };

  // Links leaving Gerdr go to the default browser (Electron: setWindowOpenHandler / will-navigate).
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
  addEventListener("click", (e) => {
    if (e.defaultPrevented || e.button > 1) return;
    const a = e.composedPath().find((n) => n instanceof HTMLAnchorElement && n.href);
    const x = a && external(a.href);
    if (!x) return;
    e.preventDefault();
    post({ type: "open", url: x });
  }); // bubble, on window: after the UI's own handlers

  // Right-click: no browser menu where the UI didn't answer with its own; only in editable fields or on selected text.
  addEventListener("contextmenu", (e) => {
    if (e.defaultPrevented) return;
    const t = e.target instanceof Element ? e.target : null;
    const editable = t && t.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"], [contenteditable="plaintext-only"]');
    if (!editable && !String(getSelection() || "")) e.preventDefault();
  });

  // The window's title follows the page's (WebView2 doesn't do it for its host window).
  const title = () => post({ type: "__title", title: document.title });
  addEventListener("DOMContentLoaded", () => {
    title();
    const t = document.querySelector("title");
    if (t) new MutationObserver(title).observe(t, { childList: true, characterData: true, subtree: true });
  });

  window.gerdrNative = {
    postMessage: (m) => (m && m.type === "notify" ? notify(m) : post(m)),
    // No getPathForFile: a dropped / picked file's path isn't reachable through this WebView2 binding, and its mere
    // presence tells the UI paths are (ui/src/lib/native.ts hasNativeFilePath). Without it the UI uploads, as in a browser.
    platform: "win32",
    shell: "webview2",
  };
})();
