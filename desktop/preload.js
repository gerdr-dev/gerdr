// 网页 → 壳 的那座桥。和原来 mac 外壳（WKWebView 的 messageHandlers.gerdr）同一个形状：
// postMessage({type, ...})，网页那边（ui/src/lib/native.ts）不用分两套。
const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("gerdrNative", {
  postMessage: (m) => ipcRenderer.send("gerdr", m),
  platform: process.platform,
});
