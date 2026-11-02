// Natural size of every image the chat has loaded on this page, by URL: images in agent replies (ChatMedia) and the
// ones attached to user messages (MessageView's SizedImg) share it. An <img> has no size until it loads (even from the
// memory cache, decoding lands a frame or more after mount), and the virtual list remounts a row each time it scrolls
// back into view, so without a reserved box the row grows back on every pass and the chat jumps (docs/dev/SCROLL.md,
// tools/scrolltest/images.mjs). Once an image has loaded once, its box is drawn from this before it loads again.

export type ImageSize = { w: number; h: number };

const sizes = new Map<string, ImageSize>();

export const imageSize = (url: string): ImageSize | undefined => sizes.get(url);

/** Records a loaded image's natural size; returns it, or undefined when the image has none (failed / SVG without one). */
export function rememberImageSize(url: string, img: HTMLImageElement): ImageSize | undefined {
  const { naturalWidth: w, naturalHeight: h } = img;
  if (!w || !h) return undefined;
  const s = { w, h };
  sizes.set(url, s);
  return s;
}
