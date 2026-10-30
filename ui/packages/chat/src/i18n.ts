import { en, zh, type MessageKey } from "./messages";

/**
 * The chat's user-visible text, in zh and en (messages.ts). The host picks the locale and may override single
 * messages (ChatProvider's locale / messages). `t` is a plain function — the transcript builds text outside
 * React too — reading what the provider set last; a locale change applies to what renders next.
 */

export type Locale = "zh" | "en";
export type { MessageKey };

let locale: Locale = "zh";
let overrides: Partial<Record<MessageKey, string>> = {};

export function setMessages(l: Locale, o?: Partial<Record<MessageKey, string>>) {
  locale = l;
  overrides = o ?? {};
}

export const currentLocale = () => locale;

/** The message for key with {name} placeholders filled from vars. */
export function t(key: MessageKey, vars?: Record<string, string | number>): string {
  let s = overrides[key] ?? (locale === "en" ? en : zh)[key] ?? zh[key] ?? key;
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.split(`{${k}}`).join(String(v));
  return s;
}
