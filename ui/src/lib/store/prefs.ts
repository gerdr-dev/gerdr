import { get, set } from "../store";
import type { Config } from "./config";

export function loadUnread(): Record<string, true> {
  try {
    return JSON.parse(localStorage.getItem("gerdr.unread") ?? "{}");
  } catch {
    return {};
  }
}

export function loadLocalFont(): number {
  try {
    return Number(localStorage.getItem("gerdr.fontSize")) || 0;
  } catch {
    return 0;
  }
}

/** 这台设备用的字号 */
export const fontSizeOf = (s: { localFont: number; config: Config }) => s.localFont || s.config.fontSize;

export function setFontSize(n: number) {
  set({ localFont: n });
  try {
    localStorage.setItem("gerdr.fontSize", String(n));
  } catch {}
}

/**
 * Settings each device keeps for itself, like the font size: one computer wants a light theme and the phone a dark one,
 * the phone's keyboard wants Enter to send, a phone has less memory for kept tabs. Stored in localStorage; a device that
 * never picked one uses the shared config's value (where they all lived before, so old choices still apply), which is
 * also what `gerdr config set` changes.
 */
export const DEVICE_KEYS = ["theme", "sendKey", "notifications", "keepTabs", "virtualList", "copyOnSelect"] as const;
export type DeviceKey = (typeof DEVICE_KEYS)[number];
export type DevicePrefs = Partial<Pick<Config, DeviceKey>>;

const DEVICE = "gerdr.device";
export function loadDevice(): DevicePrefs {
  try {
    const v = JSON.parse(localStorage.getItem(DEVICE) ?? "{}");
    return v && typeof v === "object" ? v : {};
  } catch {
    return {};
  }
}

/** The value this device uses: its own, else the shared config's. */
export const prefOf = <K extends DeviceKey>(s: { device: DevicePrefs; config: Config }, k: K): Config[K] => (s.device[k] ?? s.config[k]) as Config[K];

export function setPref(patch: DevicePrefs) {
  const device = { ...get().device, ...patch };
  set({ device });
  try {
    localStorage.setItem(DEVICE, JSON.stringify(device));
  } catch {}
}

export function loadMode(): "agents" | "work" {
  try {
    return localStorage.getItem("gerdr.mode") === "work" ? "work" : "agents";
  } catch {
    return "agents";
  }
}

export function loadSidebar(): boolean {
  try {
    return localStorage.getItem("gerdr.sidebar") !== "0";
  } catch {
    return true;
  }
}

export const SIDEBAR_W = 224;
export const SIDEBAR_MIN = 180;
export const SIDEBAR_MAX = 480;

export function loadSidebarW(): number {
  try {
    const v = Number(localStorage.getItem("gerdr.sidebarW"));
    return v ? Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, v)) : SIDEBAR_W;
  } catch {
    return SIDEBAR_W;
  }
}

export function setSidebarW(w: number) {
  const v = Math.round(Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, w)));
  if (v === get().sidebarW) return;
  set({ sidebarW: v });
  try {
    localStorage.setItem("gerdr.sidebarW", String(v));
  } catch {}
}
