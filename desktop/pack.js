// Packs the Electron App for package.sh (@electron/packager): node pack.js <arch> <name> <id> <version> <build> <shellId> <icns> <out> <resource>…
const { packager } = require("@electron/packager");
const [arch, name, id, version, build, shellId, icon, out, ...resources] = process.argv.slice(2);
packager({
  dir: __dirname,
  name,
  executableName: name,
  appBundleId: id,
  helperBundleId: id + ".helper",
  appVersion: version,
  buildVersion: build,
  platform: "darwin",
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
  darwinDarkModeSupport: true,
}).then(
  (paths) => console.log(paths.join("\n")),
  (e) => {
    console.error(e);
    process.exit(1);
  },
);
