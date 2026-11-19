/**
 * Writes text to the clipboard, for copy buttons. navigator.clipboard exists only in a secure context (https,
 * localhost; the App's http://gerdr/ counts through its unsafely-treat-insecure-origin-as-secure switch, which the
 * Electron shell went without for a while and every copy button failed). A phone or another computer opening Gerdr at
 * http://<LAN IP>:7870 still has none: there a hidden textarea and execCommand("copy") do it, as long as this runs
 * inside the click or key press that asked (execCommand needs the user's gesture). Selection copy has its own fallback
 * (copysel.ts: it copies the selection in place, which a textarea would take away).
 */
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      return await navigator.clipboard.writeText(text);
    } catch (e) {
      // Not focused, or the permission refused: the old way may still work
      if (legacyCopy(text)) return;
      throw e;
    }
  }
  if (!legacyCopy(text)) throw new Error("copy refused");
}

/** execCommand("copy") on a hidden textarea; focus goes back where it was. */
function legacyCopy(text: string): boolean {
  const was = document.activeElement as HTMLElement | null;
  const box = document.createElement("textarea");
  box.value = text;
  box.setAttribute("readonly", "");
  box.style.cssText = "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;pointer-events:none";
  document.body.appendChild(box);
  box.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  box.remove();
  was?.focus?.({ preventScroll: true });
  return ok;
}
