// Bundles the example (dependencies included) into dist/ with the package's CSS. Build the package first (../build.mjs).
import { build } from "esbuild";
import fs from "node:fs";
import path from "node:path";
const here = path.dirname(new URL(import.meta.url).pathname);
fs.mkdirSync(path.join(here, "dist"), { recursive: true });
await build({ entryPoints: [path.join(here, "main.tsx")], bundle: true, format: "esm", jsx: "automatic", outfile: path.join(here, "dist/main.js"), define: { "process.env.NODE_ENV": '"production"' }, minify: true, logLevel: "warning" });
for (const f of ["style.css", "tokens.css"]) fs.copyFileSync(path.join(here, "../dist", f), path.join(here, "dist", f));
fs.writeFileSync(
  path.join(here, "dist/index.html"),
  `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="tokens.css"><link rel="stylesheet" href="style.css">
<style>html,body,#root{height:100%;margin:0}body{background:var(--background);color:var(--foreground);font:14px -apple-system,sans-serif}*{border-color:var(--border)}</style>
</head><body><div id="root"></div><script type="module" src="main.js"></script></body></html>`,
);
console.log("example built: example/dist/index.html");
