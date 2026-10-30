// Builds the npm package into dist/ (gerdr itself serves src/ unbuilt, see the README):
//   dist/index.js     one ESM file, every dependency left as an import (react, radix-ui, streamdown… resolve in the app)
//   dist/types/       .d.ts (dist/index.d.ts points into it)
//   dist/style.css    Tailwind utilities the components use + the chat's own rules (src/style.css); no preflight
//   dist/tokens.css   gerdr's theme (light + .dark), a starting point for the CSS variables the host has to define
import { build } from "esbuild";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const here = path.dirname(new URL(import.meta.url).pathname);
const dist = path.join(here, "dist");
fs.rmSync(dist, { recursive: true, force: true });
fs.mkdirSync(dist);

await build({
  entryPoints: [path.join(here, "src/index.ts")],
  bundle: true,
  packages: "external",
  format: "esm",
  target: "es2022",
  jsx: "automatic",
  outfile: path.join(dist, "index.js"),
  sourcemap: true,
  logLevel: "warning",
});

execFileSync(path.join(here, "node_modules/.bin/tsc"), ["-p", path.join(here, "tsconfig.build.json")], { stdio: "inherit" });
fs.writeFileSync(path.join(dist, "index.d.ts"), 'export * from "./types/index";\n');

// The theme: gerdr's index.css is the one source; take its :root / .dark variables (minus the terminal's colors)
const app = fs.readFileSync(path.join(here, "../../src/index.css"), "utf8");
const block = (sel) => {
  const i = app.indexOf(`\n${sel} {`);
  const j = app.indexOf("\n}", i);
  return app
    .slice(i + 1, j + 2)
    .split("\n")
    .filter((l) => !/--term-|终端/.test(l))
    .join("\n");
};
const theme = app.slice(app.indexOf("@theme inline {"), app.indexOf("\n}", app.indexOf("@theme inline {")) + 2);
fs.writeFileSync(
  path.join(dist, "tokens.css"),
  `/* gerdr's theme for @gerdr/chat (light, and .dark on an ancestor). Copy it, or define the same variables from your own theme. */\n${block(":root")}\n\n${block(".dark")}\n`,
);

// Utilities: Tailwind 4 over the source, with the theme mapped onto the host's variables (@theme inline)
const entry = path.join(here, "dist/.style.entry.css");
fs.writeFileSync(
  entry,
  `@layer theme, base, components, utilities;
@import "tailwindcss/theme.css" layer(theme);
@import "tailwindcss/utilities.css" layer(utilities);
@custom-variant dark (&:is(.dark *));
@source "../src";
${theme}
${fs.readFileSync(path.join(here, "src/style.css"), "utf8")}`,
);
execFileSync(path.join(here, "node_modules/.bin/tailwindcss"), ["-i", entry, "-o", path.join(dist, "style.css"), "--minify"], { stdio: "inherit", cwd: here });
fs.rmSync(entry);
console.log("built", fs.readdirSync(dist).join(" "));
