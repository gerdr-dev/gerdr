// 网页 → 壳 的那座桥。和原来 mac 外壳（WKWebView 的 messageHandlers.gerdr）同一个形状：
// postMessage({type, ...})，网页那边（ui/src/lib/native.ts）不用分两套。
const { contextBridge, ipcRenderer, webUtils } = require("electron");

contextBridge.exposeInMainWorld("gerdrNative", {
  postMessage: (m) => ipcRenderer.send("gerdr", m),
  // A picked / pasted / dropped File's path on this machine (the page can't see it since File.path went away):
  // lets the chat attach any file or folder by its path instead of uploading it. "" for files not on disk.
  getPathForFile: (f) => webUtils.getPathForFile(f),
  platform: process.platform,
});
