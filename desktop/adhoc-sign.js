// electron-builder 的 afterPack 钩子：mac 上 App 组装好之后、打 dmg 之前补一个 ad-hoc 签名。
//
// 没配签名证书时 electron-builder 不签（package.json 里 identity: null），App 里只剩 Electron 自带的那份链接器签名，
// 而我们往里塞了后端、改了 Info.plist，那份签名对不上整个包：Apple 芯片上起不来，下载来的还会被当成「已损坏」。
// 原来是 build.sh 等 electron-builder 跑完再签 dist/ 里那份 App，可 dmg 那时已经打好了，装进 dmg 的仍是没签的（踩过）。
// afterSign 在「没签名」时会被直接跳过，所以挂在 afterPack 上。
// 配了 CSC_LINK（正式证书）就不管：electron-builder 接下来会正经签名 + 公证。
const { execFileSync } = require("node:child_process");
const path = require("node:path");

exports.default = async function (ctx) {
  if (ctx.electronPlatformName !== "darwin" || process.env.CSC_LINK) return;
  const app = path.join(ctx.appOutDir, `${ctx.packager.appInfo.productFilename}.app`);
  execFileSync("codesign", ["--force", "--deep", "--sign", "-", app], { stdio: "inherit" });
};
