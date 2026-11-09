// The window.gerdrNative the UI knows (ui/src/lib/native.ts; desktop/preload.js gave it under Electron), on top of
// Lightron's window.lightron (Options.Script: it runs in every page before the page's own scripts). Lightron does the
// rest the old bridge did: links to the default browser, the right-click rule, the window title.
(() => {
  if (window.gerdrNative || !window.lightron) return;

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
        lightron.focus();
        if (m.sessionId && window.gerdr && window.gerdr.focusSession) window.gerdr.focusSession(String(m.sessionId));
      };
      n.onclose = () => live.delete(n);
    };
    if (Notification.permission === "granted") show();
    else if (Notification.permission !== "denied") Notification.requestPermission().then((p) => p === "granted" && show());
  };

  window.gerdrNative = {
    postMessage: (m) => (m && m.type === "notify" ? notify(m) : lightron.send(m)),
    // No getPathForFile: a dropped / picked file's path isn't reachable through WebView2 here, and its mere presence
    // tells the UI paths are (ui/src/lib/native.ts hasNativeFilePath). Without it the UI uploads, as in a browser.
    platform: "win32",
    shell: "webview2",
  };
})();
