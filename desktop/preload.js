// The bridge the UI knows (ui/src/lib/native.ts): window.gerdrNative, the same shape the Lightron and WebView2 shells
// gave (postMessage({type, …})), so the page has one set of messages.
const { contextBridge, ipcRenderer, webUtils } = require("electron");

contextBridge.exposeInMainWorld("gerdrNative", {
  postMessage: (m) => ipcRenderer.send("gerdr", m),
  // A picked / pasted / dropped File's path on this machine: the chat attaches files and folders by their path
  getPathForFile: (f) => webUtils.getPathForFile(f),
  // a still of a project's page (tab: the backend's id), a data URL, straight from the main process (main.js)
  browserShot: (tab) => ipcRenderer.invoke("gerdr:shot", tab),
  // Generic ways to the main-process script (internal/desktop/shell/main.js), so a new feature there needs no change
  // here (this file is part of the frozen App: docs/dev/SHELL.md). invoke: ipcMain.handle("gerdr:" + name) there;
  // on: what it sends with webContents.send("gerdr:" + name, …); returns the way to stop listening.
  invoke: (name, ...args) => ipcRenderer.invoke("gerdr:" + String(name), ...args),
  on: (name, cb) => {
    const ch = "gerdr:" + String(name);
    const f = (_e, ...args) => cb(...args);
    ipcRenderer.on(ch, f);
    return () => ipcRenderer.removeListener(ch, f);
  },
  platform: process.platform,
  shell: "electron",
  // the window has no system title bar (macOS: only the traffic lights over the page, main.js trafficLightPosition):
  // their row's height and how far right they reach (ui/src/lib/native.ts nativeTitleBar)
  titleBar: process.platform === "darwin" ? { height: 40, right: 72 } : null,
});
