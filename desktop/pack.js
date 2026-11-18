// Packs the Electron App for package.sh / package-win.sh (@electron/packager):
//   node pack.js <platform> <arch> <name> <id> <version> <build> <shellId> <icon> <out> <resource>…
// Writes gerdr-app.json (version, shell id, id) beside the resources: main.js reads it on every platform (Windows has
// no Info.plist, and app.getVersion() there is package.json's).
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { packager } = require("@electron/packager");
const [platform, arch, name, id, version, build, shellId, icon, out, ...resources] = process.argv.slice(2);
const meta = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "gerdr-pack-")), "gerdr-app.json");
fs.writeFileSync(meta, JSON.stringify({ version, shell: shellId, id }));
resources.push(meta);
// Windows' file version is numbers only (1.2.3.4): a git describe one (0.2.47-21-gabc-dirty) is cut to its x.y.z
const winVersion = (version.match(/^\d+\.\d+\.\d+/) || ["0.0.0"])[0];
packager({
  dir: __dirname,
  name,
  executableName: name,
  appBundleId: id,
  helperBundleId: id + ".helper",
  appVersion: platform === "win32" ? winVersion : version,
  buildVersion: platform === "win32" ? winVersion : build,
  platform,
  arch,
  out,
  overwrite: true,
  icon,
  asar: true,
  prune: true,
  // only the App's own files go in: main.js, preload.js, package.json
  ignore: [/^\/core/, /^\/dist/, /^\/bin/, /^\/res/, /\.sh$/, /^\/compress\.js$/, /^\/pack\.js$/, /^\/\.gitignore$/],
  extraResource: resources,
  extendInfo: { GerdrShell: shellId, LSMinimumSystemVersion: "12.0", NSHumanReadableCopyright: "Gerdr" },
  win32metadata: { CompanyName: "Gerdr", FileDescription: name, ProductName: name, InternalName: name, OriginalFilename: name + ".exe" },
  darwinDarkModeSupport: true,
}).then(
  (paths) => console.log(paths.join("\n")),
  (e) => {
    console.error(e);
    process.exit(1);
  },
);
