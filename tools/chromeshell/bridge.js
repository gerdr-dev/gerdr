// The window.gerdrNative the UI knows (ui/src/lib/native.ts; desktop/preload.js gave it under Electron), on top of
// Lightron's window.lightron (Options.Script: it runs in every page before the page's own scripts). Lightron already
// does what this bridge used to: files' paths, links to the default browser, the right-click rule.
(() => {
  if (window.gerdrNative || !window.lightron) return;
  window.gerdrNative = {
    postMessage: (m) => lightron.send(m),
    getPathForFile: (f) => lightron.getPathForFile(f),
    platform: lightron.platform === "windows" ? "win32" : lightron.platform,
    shell: "chrome",
  };
})();
