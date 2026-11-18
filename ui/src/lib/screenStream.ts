// The picture stream shared by Settings → Remote access → Control this computer (components/RemoteScreen.tsx) and the
// Browser pane (components/browser/): the backend sends only the tiles that changed (internal/screen/diff.go,
// server/screenstream.go), each message a header and the tiles' JPEGs back to back, pasted onto a canvas here.

/** Where a browser tab is (the Browser pane's address bar); sent when it changes. */
export type StreamPage = { url: string; title: string; back?: boolean; forward?: boolean; loading?: boolean };

export type StreamFrame = {
  seq: number;
  w: number;
  h: number;
  full?: boolean;
  ack: number;
  cur?: [number, number];
  rects: [number, number, number, number, number][];
  err?: string;
  inputErr?: string;
  page?: StreamPage;
  /** the selection a {t:"copy"} asked for (a browser tab) */
  text?: string;
};

/** Reads one stream message: a 4-byte big-endian header length, the header JSON, then the rects' JPEGs back to back. */
export function parseFrame(buf: ArrayBuffer): { h: StreamFrame; body: number } {
  const n = new DataView(buf).getUint32(0);
  return { h: JSON.parse(new TextDecoder().decode(new Uint8Array(buf, 4, n))) as StreamFrame, body: 4 + n };
}

/**
 * Pastes a frame's tiles onto the canvas (resizing it for a full frame). The tiles decode in parallel, then go on
 * together, so a frame never shows half drawn. Returns whether the canvas was (re)sized: a full frame or a new size.
 */
export async function paintFrame(cv: HTMLCanvasElement, buf: ArrayBuffer, h: StreamFrame, body: number): Promise<boolean> {
  let off = body;
  const bitmaps = await Promise.all(
    h.rects.map(([, , , , len]) => {
      const blob = new Blob([new Uint8Array(buf, off, len)], { type: "image/jpeg" });
      off += len;
      return createImageBitmap(blob);
    }),
  );
  const sized = !!h.full || cv.width !== h.w || cv.height !== h.h;
  if (sized) {
    cv.width = h.w;
    cv.height = h.h;
  }
  const ctx = cv.getContext("2d");
  h.rects.forEach(([x, y], i) => {
    ctx?.drawImage(bitmaps[i], x, y);
    bitmaps[i].close();
  });
  return sized;
}

/** ws(s)://<this host><path> */
export function wsURL(path: string): string {
  return `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}${path}`;
}
