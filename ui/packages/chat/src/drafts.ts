import { useSyncExternalStore } from "react";

/**
 * A composer's unsent text and attachments, per paneId (one composer each). The host can keep them in its own store
 * (ChatHost.composer.drafts) so a draft outlives the composer (switching tabs, a reload); without one they live in
 * memory here.
 *
 * Attachments: an image keeps its bytes in `data` (base64); a video can't go inline, so it's uploaded
 * (ChatHost.upload) and sent as a resource_link to `path` — unset while the upload runs, `data` empty. `blob`: the
 * host stored the image and can show it elsewhere (gerdr's shared drafts); `data` may be empty then until it's sent.
 */
export type DraftImage = { data: string; mimeType: string; url: string; path?: string; name?: string; blob?: string };

export interface DraftStore {
  useText(paneId: string): string;
  getText(paneId: string): string;
  setText(paneId: string, text: string): void;
  useImages(paneId: string): DraftImage[];
  getImages(paneId: string): DraftImage[];
  /** f gets the latest list: callbacks (a file read, an upload) may land after the composer unmounted. */
  setImages(paneId: string, f: DraftImage[] | ((cur: DraftImage[]) => DraftImage[])): void;
}

const NONE: DraftImage[] = [];
const texts = new Map<string, string>();
const images = new Map<string, DraftImage[]>();
const subs = new Set<() => void>();
const changed = () => subs.forEach((f) => f());
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));

export const memoryDrafts: DraftStore = {
  useText: (id) => useSyncExternalStore(subscribe, () => texts.get(id) ?? ""),
  getText: (id) => texts.get(id) ?? "",
  setText: (id, t) => (texts.set(id, t), changed()),
  useImages: (id) => useSyncExternalStore(subscribe, () => images.get(id) ?? NONE),
  getImages: (id) => images.get(id) ?? NONE,
  setImages: (id, f) => (images.set(id, typeof f === "function" ? f(images.get(id) ?? NONE) : f), changed()),
};
