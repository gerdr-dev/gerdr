// The bridge the UI knows (ui/src/lib/native.ts): window.gerdrNative, the same shape the Lightron and WebView2 shells
// gave (postMessage({type, …})), so the page has one set of messages.
const { contextBridge, ipcRenderer, webUtils } = require("electron");

contextBridge.exposeInMainWorld("gerdrNative", {
  postMessage: (m) => ipcRenderer.send("gerdr", m),
  // A picked / pasted / dropped File's path on this machine: the chat attaches files and folders by their path
  getPathForFile: (f) => webUtils.getPathForFile(f),
  // a still of a project's page (tab: the backend's id), a data URL, straight from the main process (main.js)
  browserShot: (tab) => ipcRenderer.invoke("gerdr:shot", tab),
  platform: process.platform,
  shell: "electron",
  // the window has no system title bar (macOS: only the traffic lights over the page, main.js trafficLightPosition):
  // their row's height and how far right they reach (ui/src/lib/native.ts nativeTitleBar)
  titleBar: process.platform === "darwin" ? { height: 40, right: 72 } : null,
});
