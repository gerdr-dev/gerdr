import { useEffect } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { androidCan, biometricName, enrollAppKey, isAndroid, isApp, isIOSApp, shellReady } from "@/lib/android";
import { t } from "@/lib/i18n";
import { isCancel, isStepUp, passkeySupported, registerPasskey } from "@/lib/passkey";
import { set } from "@/lib/store";

/**
 * Right after a device comes in with a pairing code it is temporary (auth.Device.Temp, gone after pairMinutes).
 * Ask once whether to keep it: a passkey in a browser, the App's Face ID / fingerprint key in the phone Apps. Both
 * are optional — the person may only want this device for the job at hand, which is why the Apps no longer turn
 * Face ID on by themselves after pairing (user decision, 2026-11-17). Declining just closes it; Settings →
 * Remote access can do it later (with an approval on the computer once the minutes after pairing are over).
 *
 * Asked only while the pairing is fresh (whoami appKeyFresh) and once per device (localStorage), so a reload or a
 * later visit doesn't nag.
 *
 * A toast, not a dialog (user's pick): it's optional, so it shouldn't dim the page and demand an answer. It stays
 * until answered. The approval on the computer (once the minutes after pairing are over) needs a code and a wait,
 * which a toast can't hold: Settings → Remote access has that flow, so "Add" opens it then.
 */

export type KeepMethod = "passkey" | "bio";

/** How this device can be kept: the App's biometric key where the shell has one, else a passkey where it works. */
export async function keepMethod(passkeyAvailable: boolean): Promise<KeepMethod | null> {
  if (isApp()) {
    if ((await shellReady()) && androidCan("appKey")) return "bio";
    // iOS App: no passkeys in its WebView; Android App: only when its WebView can do them itself
    if (isIOSApp() || (isAndroid() && !androidCan("webauthn"))) return null;
  }
  return passkeyAvailable && passkeySupported() ? "passkey" : null;
}

const askedKey = (device: string) => `gerdr.keepAsked:${device}`;

const ID = "keep-device";

export function KeepDevicePrompt() {
  useEffect(() => {
    let dead = false;
    (async () => {
      const w = await api.remoteWhoami().catch(() => null);
      if (!w?.remote || !w.authed || !w.temp || !w.appKeyFresh || !w.deviceId) return;
      if (localStorage.getItem(askedKey(w.deviceId))) return;
      const method = await keepMethod(w.passkeyAvailable);
      if (!method || dead) return;
      localStorage.setItem(askedKey(w.deviceId), "1");
      askToast(method, w.expires);
    })();
    return () => {
      dead = true;
    };
  }, []);
  return null;
}

function askToast(method: KeepMethod, expires?: string, err?: string) {
  const bio = method === "bio";
  const what = bio ? biometricName() : "Passkey";
  // Minutes left on the temporary device, as the person reads them ("expires in 28 minutes")
  const n = expires ? Math.max(1, Math.ceil((new Date(expires).getTime() - Date.now()) / 60000)) : 0;
  const lines = [n > 0 ? t("keepDevice.body", { n }) : "", t("keepDevice.later"), err ?? ""].filter(Boolean);
  const toSettings = () => {
    toast.dismiss(ID);
    set({ dialog: { kind: "settings", tab: "remote" } });
  };
  const add = async () => {
    toast.loading(t("keepDevice.busy"), { id: ID, description: undefined, action: undefined, cancel: undefined, closeButton: false });
    try {
      if (bio) {
        // Past the window the key would be refused: the approval on the computer is in Settings
        if (!(await api.remoteWhoami()).appKeyFresh) return toSettings();
        await enrollAppKey();
      } else {
        await registerPasskey();
      }
      toast.success(t("keepDevice.doneTitle"), {
        id: ID,
        description: bio ? t("keepDevice.done", { what }) : t("keepDevice.donePasskey"),
        duration: 4000,
        closeButton: false,
      });
    } catch (e) {
      if (isStepUp(e)) return toSettings();
      askToast(method, expires, isCancel(e) ? undefined : (e as Error).message);
    }
  };
  toast(bio ? t("keepDevice.title", { what }) : t("keepDevice.titlePasskey"), {
    id: ID,
    duration: Infinity,
    closeButton: true,
    // Buttons under the text, right-aligned: beside it they squeezed the text into a narrow column on a phone
    classNames: { toast: "flex-wrap!", content: "basis-full!", cancelButton: "ml-auto!", actionButton: "ml-2!" },
    description: lines.join(" "),
    // preventDefault: sonner closes the toast on a button; this one turns into the progress / result instead
    action: {
      label: t("keepDevice.add"),
      onClick: (e) => {
        e.preventDefault();
        add();
      },
    },
    cancel: { label: t("keepDevice.skip"), onClick: () => {} },
  });
}
