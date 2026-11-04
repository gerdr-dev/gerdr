// g.audio: one player for every add-on. Sound is a capability, not a feature: reading a message aloud (an add-on's
// g.messageAction calling a speech API), a chime, a recording all play through here, so the hard parts are done once:
// - one sound at a time: a new play() ends the one before (its promise resolves with false), so two add-ons, or two
//   messages read one after another, never talk over each other;
// - the browser's autoplay rules (unlock below);
// - clips one after another with no gaps: the next is asked for as soon as one starts playing.
// A play() given the signal of a message action's run (ctx.signal) also tells that action's button that sound is on,
// so it shows "stop" instead of a spinner (lib/messageActions.ts).
import { t } from "./i18n";

/** A clip: bytes or a Blob of audio, or a URL to it (https:, blob:; the page's CSP has no data: for media). */
export type Clip = string | Blob | ArrayBuffer | ArrayBufferView;
export type Clips = Clip | AsyncIterable<Clip> | Iterable<Clip>;

// One <audio> for every clip. Browsers only play sound started by a tap, and by the time an add-on has fetched its
// audio the tap is long gone (Android's WebView requires a gesture for every element it hasn't played yet). Playing a
// silent clip on this element during a tap blesses it, and later sources on the same element play.
let el: HTMLAudioElement | null = null;
let silent = "";
// The play() going on now: stopping it resolves its promise
let current: AbortController | null = null;
// Who to tell when sound starts / stops for a signal (a message action's run, lib/messageActions.ts)
const watchers = new WeakMap<AbortSignal, (on: boolean) => void>();

export function watchSignal(s: AbortSignal, fn: (on: boolean) => void) {
  watchers.set(s, fn);
}

/** Call inside a tap (a message action's click does): later plays are then allowed without one. */
export function unlockAudio() {
  if (!el) el = new Audio();
  if (!silent) silent = URL.createObjectURL(new Blob([silentWav()], { type: "audio/wav" }));
  if (current) return; // something is playing: the element is blessed already, don't cut it off
  el.src = silent;
  el.play().catch(() => {});
}

// 50ms of silence, 8 kHz 8-bit mono.
function silentWav(): ArrayBuffer {
  const n = 400;
  const b = new DataView(new ArrayBuffer(44 + n));
  const str = (o: number, s: string) => [...s].forEach((c, i) => b.setUint8(o + i, c.charCodeAt(0)));
  str(0, "RIFF");
  b.setUint32(4, 36 + n, true);
  str(8, "WAVEfmt ");
  b.setUint32(16, 16, true);
  b.setUint16(20, 1, true);
  b.setUint16(22, 1, true);
  b.setUint32(24, 8000, true);
  b.setUint32(28, 8000, true);
  b.setUint16(32, 1, true);
  b.setUint16(34, 8, true);
  str(36, "data");
  b.setUint32(40, n, true);
  for (let i = 0; i < n; i++) b.setUint8(44 + i, 128);
  return b.buffer;
}

/** Stop whatever plays. */
export function stopAudio() {
  current?.abort();
  current = null;
  el?.pause();
}

const isClip = (a: unknown): a is Clip => typeof a === "string" || a instanceof Blob || a instanceof ArrayBuffer || ArrayBuffer.isView(a);

/**
 * Play a clip, or clips in order. Resolves true when it played to the end, false when stopped: by signal, by another
 * play(), or by stopAudio(). A clip that can't be played, or an iterable that throws, rejects.
 */
export async function playAudio(clips: Clips, opts?: { signal?: AbortSignal; onClip?: (index: number) => void }): Promise<boolean> {
  stopAudio();
  const c = new AbortController();
  current = c;
  const outer = opts?.signal;
  const stop = () => c.abort();
  // Stopped from outside (the action's stop): silence it now, not at the end of the clip
  c.signal.addEventListener("abort", () => current === c && el?.pause(), { once: true });
  if (outer?.aborted) return false;
  outer?.addEventListener("abort", stop, { once: true });
  const tell = outer && watchers.get(outer);
  try {
    // onClip: told when each clip starts sounding, so an add-on can follow along (mark the paragraph being read)
    const started = (i: number) => {
      tell?.(true);
      try {
        opts?.onClip?.(i);
      } catch (e) {
        console.error(e);
      }
    };
    if (isClip(clips)) await playOne(clips, c, () => started(0));
    else await playAll(clips, c, started);
    return !c.signal.aborted;
  } finally {
    outer?.removeEventListener("abort", stop);
    tell?.(false);
    if (current === c) current = null;
  }
}

async function playAll(r: AsyncIterable<Clip> | Iterable<Clip>, c: AbortController, started: (index: number) => void) {
  const it = Symbol.asyncIterator in r ? r[Symbol.asyncIterator]() : (r as Iterable<Clip>)[Symbol.iterator]();
  try {
    let next = Promise.resolve(it.next());
    for (let i = 0; ; i++) {
      // Stopped while the last clip played: the next one asked for ahead may have failed because of that very stop
      // (an add-on cancelling its synthesis), so don't wait on it, let alone throw what it threw
      if (c.signal.aborted) break;
      const { value, done } = await next;
      if (done || c.signal.aborted) break;
      // Ask for the next while this one plays, so a generator synthesizes ahead
      next = Promise.resolve(it.next());
      next.catch(() => {}); // seen at the next await
      await playOne(value, c, () => started(i));
    }
  } finally {
    // Let a generator clean up (cancel its pending request) when stopped halfway
    if (c.signal.aborted) await Promise.resolve(it.return?.()).catch(() => {});
  }
}

function playOne(a: Clip, c: AbortController, started: () => void): Promise<void> {
  if (c.signal.aborted) return Promise.resolve();
  const own = typeof a !== "string";
  const url = own ? URL.createObjectURL(a instanceof Blob ? a : new Blob([a as BlobPart])) : a;
  const audio = el ?? (el = new Audio());
  return new Promise<void>((ok, no) => {
    const done = () => {
      audio.onended = audio.onerror = null;
      c.signal.removeEventListener("abort", done);
      ok();
    };
    audio.onended = done;
    audio.onerror = () => {
      done();
      no(new Error(t("audio.bad")));
    };
    c.signal.addEventListener("abort", done, { once: true });
    audio.src = url;
    audio.play().then(
      () => started(),
      (e) => {
        done();
        no(e);
      },
    );
  }).finally(() => own && URL.revokeObjectURL(url));
}
