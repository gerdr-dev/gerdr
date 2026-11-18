// The bridge the UI knows (ui/src/lib/native.ts): window.gerdrNative, the same shape the Lightron and WebView2 shells
// gave (postMessage({type, …})), so the page has one set of messages.
const { contextBridge, ipcRenderer, webUtils } = require("electron");

contextBridge.exposeInMainWorld("gerdrNative", {
  postMessage: (m) => ipcRenderer.send("gerdr", m),
  // A picked / pasted / dropped File's path on this machine: the chat attaches files and folders by their path
  getPathForFile: (f) => webUtils.getPathForFile(f),
  platform: process.platform,
  shell: "electron",
  // the window has no system title bar (macOS: only the traffic lights over the page): the page keeps a strip this
  // high at its top, draggable, with nothing to click under the lights (desktop/main.js titleBarStyle)
  titleBar: process.platform === "darwin" ? { height: 30, left: 78 } : null,
});
