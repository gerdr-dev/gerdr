/**
 * Pictures people upload as a project's or panel's icon (IconPicker). Kept inline as a data URL in the icon value
 * itself, not as a file: the value already travels everywhere it has to (layout.json for a project, the panel's
 * user/ui.json, another computer through /h/<host>, the phone) and a file would need an address served behind the
 * auth guard on each of them. So the picture is cut to a square and shrunk to ICON_PX first, which keeps it a few KB;
 * the backend refuses anything much bigger (server/projectop.go iconError, the same limit).
 */

/** A text icon's value: "text:" and up to three characters. */
export const TEXT_ICON = "text:";

/** Edge of an uploaded icon, in pixels: the largest it's drawn (the rail's 36px button) at 3x. */
export const ICON_PX = 112;

export const isImageIcon = (icon?: string) => !!icon && /^data:image\/(png|jpeg|webp|gif);base64,/.test(icon);

/** The picture as an icon value: the middle square of it at ICON_PX, WebP where the browser can write it (PNG else). */
export async function imageToIcon(file: Blob): Promise<string> {
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    img.src = url;
    await img.decode();
    // An SVG without a size reports 0: draw it whole at the icon's size
    const w = img.naturalWidth || ICON_PX;
    const h = img.naturalHeight || ICON_PX;
    const side = Math.min(w, h);
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = ICON_PX;
    const g = canvas.getContext("2d");
    if (!g) throw new Error("no canvas");
    g.imageSmoothingQuality = "high";
    g.drawImage(img, (w - side) / 2, (h - side) / 2, side, side, 0, 0, ICON_PX, ICON_PX);
    // Safari can't encode WebP and hands back PNG for it, which is fine too
    const out = canvas.toDataURL("image/webp", 0.9);
    return out.startsWith("data:image/webp") ? out : canvas.toDataURL("image/png");
  } finally {
    URL.revokeObjectURL(url);
  }
}
