import type { Locale } from "@gerdr/chat";
import { en, zh, type MessageKey } from "./messages";
import type { AddonSetting, AddonText } from "./types";

// The interface's language. Each device keeps its own choice (localStorage, like the font size): a phone set to English
// and a computer in Chinese are both right, and "follow the system" can only mean this device's system anyway.
//
// The language is fixed for the life of the page: t() is a plain function (toasts, the command palette and the store
// build text outside React), so a change reloads the page rather than chasing every place that cached a string.
// Changing it is rare; a reload is the one way nothing stays in the old language.

export type { Locale };
/** What the user picked: a language, or "system" (this device's system language; zh for any Chinese, else en). */
export type LanguagePref = "system" | Locale;

const KEY = "gerdr.language";

export function languagePref(): LanguagePref {
  try {
    const v = localStorage.getItem(KEY);
    return v === "zh" || v === "en" ? v : "system";
  } catch {
    return "system";
  }
}

export function systemLocale(): Locale {
  const langs = typeof navigator === "undefined" ? [] : navigator.languages?.length ? navigator.languages : [navigator.language];
  return (langs[0] ?? "").toLowerCase().startsWith("zh") ? "zh" : "en";
}

const resolve = (p: LanguagePref): Locale => (p === "system" ? systemLocale() : p);

let current: Locale = resolve(languagePref());
if (typeof document !== "undefined") document.documentElement.lang = current === "zh" ? "zh-CN" : "en";

/** The language the interface is in. */
export const locale = (): Locale => current;

/** Pick a language: reloads the page when that changes what's shown. */
export function setLanguage(p: LanguagePref) {
  try {
    if (p === "system") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, p);
  } catch {}
  if (resolve(p) !== current) location.reload();
}

// "Follow the system" follows it while open too (the system language changed under the page)
if (typeof window !== "undefined")
  window.addEventListener("languagechange", () => {
    if (languagePref() === "system" && systemLocale() !== current) location.reload();
  });

/** The message for key in the interface's language, {name} placeholders filled from vars. */
export function t(key: MessageKey, vars?: Record<string, string | number>): string {
  let s = (current === "en" ? en : zh)[key] ?? zh[key] ?? key;
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.split(`{${k}}`).join(String(v));
  return s;
}

type Localizable = {
  name: string;
  description?: string;
  settings?: AddonSetting[];
  readme?: string;
  i18n?: Record<string, AddonText>;
  readmes?: Record<string, string>;
};

/**
 * An add-on (installed or in a store) with addon.json's i18n entry for the interface's language laid over its base
 * texts, and README.<locale>.md in place of README.md. Missing pieces keep the base text, so a half-translated add-on
 * still shows everything.
 */
export function localizeAddon<T extends Localizable>(a: T): T {
  const tx = a.i18n?.[current];
  const readme = a.readmes?.[current];
  if (!tx && !readme) return a;
  return {
    ...a,
    name: tx?.name || a.name,
    description: tx?.description || a.description,
    readme: readme ?? a.readme,
    settings: a.settings?.map((s) => {
      const st = tx?.settings?.[s.key];
      if (!st) return s;
      return {
        ...s,
        title: st.title || s.title,
        description: st.description || s.description,
        unit: st.unit || s.unit,
        options: s.options?.map((o) => ({ ...o, label: st.options?.[String(o.value)] ?? o.label })),
      };
    }),
  };
}
