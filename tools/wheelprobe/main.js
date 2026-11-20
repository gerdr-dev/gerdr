// Records how Electron (Gerdr's Chromium) scrolls under a real mouse wheel: every wheel event and every frame's
// scrollTop, to /tmp/electron-wheel.log. Run: <electron> tools/wheelprobe
const { app, BrowserWindow } = require("electron");
app.whenReady().then(() => {
  const w = new BrowserWindow({ width: 900, height: 900, title: "wheel probe (Electron)", webPreferences: { nodeIntegration: true, contextIsolation: false } });
  w.loadFile(require("path").join(__dirname, "index.html"));
});
app.on("window-all-closed", () => app.quit());
