// The shell's language (menus, dialogs, notifications it shows itself): the same as the interface's on this computer.
//
// The interface keeps its choice per device in the page's localStorage (ui/src/lib/i18n.ts), out of the shell's
// reach, so each page reports the language it shows ({type: "locale"}, ui/src/lib/native.ts) and the shell remembers
// it in locale.json: the dialogs shown before any page loads ("starting…", the backend failed) then match too.
// Nothing reported yet (first launch, an older interface) = the system's language, which is the interface's default.
//
// Text sits in the code as pairs, s("中文", "English"), like the backend's i18n.S.
const { app } = require("electron");
const fs = require("node:fs");
const path = require("node:path");

const file = () => path.join(app.getPath("userData"), "locale.json");
const valid = (l) => (l === "zh" || l === "en" ? l : null);

function systemLocale() {
  const l = (app.getPreferredSystemLanguages?.()[0] || app.getLocale?.() || "").toLowerCase();
  return l.startsWith("zh") ? "zh" : "en";
}

function saved() {
  try {
    return valid(JSON.parse(fs.readFileSync(file(), "utf8")).locale);
  } catch {
    return null;
  }
}

let current = null;

/** "zh" | "en". */
function locale() {
  return (current ??= saved() ?? systemLocale());
}

/** A page reported its language: returns true when that changed it (the caller rebuilds the menu). */
function setLocale(l) {
  l = valid(l);
  if (!l || l === locale()) return false;
  current = l;
  try {
    fs.writeFileSync(file(), JSON.stringify({ locale: l }));
  } catch {}
  return true;
}

/** zh or en by the shell's language. */
const s = (zh, en) => (locale() === "zh" ? zh : en);

module.exports = { locale, setLocale, s };
