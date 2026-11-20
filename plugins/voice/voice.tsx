// The voice plugin's work (from gerdr-addon/voice): Doubao streaming recognition fed by the host's microphone, and
// reading aloud (Doubao / Azure TTS) played by the host. The UI and the glue to Gerdr are in App.tsx.
//
// What differs from the add-on: the microphone. TSX has no raw audio capture, so the PCM comes from the host app
// (Gerdr's Android app, NshSpikeActivity: AudioRecord over `host.call("mic.start")` / `host.on("mic.pcm")`). And the
// sockets / requests go straight to Doubao with their headers (the add-on goes through the backend: a page can't set
// WebSocket headers). Settings are the add-on's own (GET /api/addons → voice.settingValues).
import { signal, computed, host, Text } from "nsh";
import { Button } from "nsh/base";
import { connect } from "websocket";
import { gunzipSync } from "zlib";

export type Settings = Record<string, any>;
/** The add-on's settings, as the main UI hands them over (the slot's ctx) */
export const settings = signal<Settings>({});
const voiceSettings = async () => settings();



// ---------- recognition: Doubao streaming ASR 2.0 (the add-on's doubaoLive) ----------

/** Doubao's binary frame: 4-byte header (version | header size, type | flags, serialization | compression, 0), size, payload. */
function frame(type: number, flags: number, json: boolean, payload: Uint8Array) {
  const out = new Uint8Array(8 + payload.length);
  out.set([0x11, (type << 4) | flags, json ? 0x10 : 0x00, 0]);
  new DataView(out.buffer).setUint32(4, payload.length);
  out.set(payload, 8);
  return out;
}

export type Dictation = { stop: () => Promise<void>; cancel: () => void };

/** Starts listening: `onText(settled, live)` as the recognizer hears; resolves once the mic and the socket are open. */
export async function dictate(hints: string[], onText: (text: string) => void, onError: (e: string) => void): Promise<Dictation> {
  const s = await voiceSettings();
  const key = (s.doubaoKey ?? "").trim();
  const appId = (s.doubaoAppId ?? "").trim();
  if (!key) throw new Error("先在语音插件的设置里填豆包的 Key");
  const headers: Record<string, string> = {
    "X-Api-Resource-Id": "volc.seedasr.sauc.duration",
    "X-Api-Connect-Id": crypto.randomUUID(),
    "X-Api-Request-Id": crypto.randomUUID(),
  };
  if (appId) Object.assign(headers, { "X-Api-App-Key": appId, "X-Api-Access-Key": key });
  else headers["X-Api-Key"] = key;
  console.log("asr: connecting");
  const sock = await connect("wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async", { headers });
  console.log("asr: connected");
  const words = [...String(s.words ?? "").split("\n"), ...hints].map((w) => w.trim()).filter(Boolean);
  const body = {
    user: { uid: "gerdr" },
    audio: { format: "pcm", rate: 16000, bits: 16, channel: 1 },
    request: {
      model_name: "bigmodel",
      enable_itn: true,
      enable_punc: true,
      enable_nonstream: true,
      result_type: "full",
      show_utterances: true,
      ...(words.length ? { corpus: { context: JSON.stringify({ hotwords: words.slice(0, 30).map((word) => ({ word })) }) } } : {}),
    },
  };
  await sock.write(frame(1, 0, true, new TextEncoder().encode(JSON.stringify(body))));

  // Audio: the host's ~100ms chunks, two per frame (~200ms, as the API prefers)
  let held: Uint8Array[] = [];
  let heldLen = 0;
  const flush = (last: boolean) => {
    const all = new Uint8Array(heldLen);
    let at = 0;
    for (const c of held) (all.set(c, at), (at += c.length));
    held = [];
    heldLen = 0;
    return sock.write(frame(2, last ? 2 : 0, false, all)).catch(() => {});
  };
  let chunks = 0;
  const off = host.on<string>("mic.pcm", (b64) => {
    if (chunks++ % 10 === 0) {
      const b = Buffer.from(b64, "base64");
      const v = new Int16Array(b.buffer, b.byteOffset, b.length >> 1);
      let peak = 0;
      for (const x of v) peak = Math.max(peak, Math.abs(x));
      console.log(`asr: pcm chunk ${chunks}, peak ${peak}`);
    }
    const b = Buffer.from(b64, "base64");
    held.push(new Uint8Array(b.buffer, b.byteOffset, b.length));
    heldLen += b.length;
    if (heldLen >= 6400) flush(false);
  });

  let done!: () => void;
  const finished = new Promise<void>((r) => (done = r));
  (async () => {
    for (let m; (m = await sock.read()) !== null; ) {
      if (typeof m === "string") continue;
      const v = new DataView(m.buffer, m.byteOffset, m.byteLength);
      const type = v.getUint8(1) >> 4;
      const flags = v.getUint8(1) & 15;
      console.log(`asr: frame type ${type} flags ${flags} ${m.length} bytes`);
      if (type === 0b1111) {
        const msg = new TextDecoder().decode(m.subarray(12, 12 + v.getUint32(8)));
        onError(`豆包 ${v.getUint32(4)}: ${msg}`);
        break;
      }
      if (type !== 0b1001) continue;
      let at = 4;
      if (flags & 1) at += 4; // a sequence number
      let bytes = m.subarray(at + 4, at + 4 + v.getUint32(at));
      if ((v.getUint8(2) & 15) === 1) bytes = new Uint8Array(gunzipSync(bytes));
      let res: any;
      try {
        res = JSON.parse(new TextDecoder().decode(bytes));
      } catch {
        continue;
      }
      // "full" results: each holds everything heard so far
      if (res?.result?.text !== undefined) {
        console.log(`asr: text "${res.result.text}"`);
        onText(res.result.text);
      }
      if (flags & 2) break; // the last response
    }
    console.log(`asr: read loop ended (close ${sock.closeCode} ${sock.closeReason})`);
    done();
  })().catch((e) => (console.log(`asr: read error ${e?.message ?? e}`), onError(String(e?.message ?? e)), done()));

  await host.call("mic.start");
  console.log("asr: mic open");
  return {
    async stop() {
      await host.call("mic.stop").catch(() => {});
      off();
      await flush(true);
      // the final result comes a moment after the last audio
      await Promise.race([finished, new Promise((r) => setTimeout(r, 3000))]);
      sock.close().catch(() => {});
    },
    cancel() {
      host.call("mic.stop").catch(() => {});
      off();
      sock.close().catch(() => {});
    },
  };
}

// ---------- reading aloud (the add-on's say: Doubao / Azure; MiMo left out of the spike) ----------

const xml = (t: string) => t.replace(/[<>&'"]/g, (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", "'": "&apos;", '"': "&quot;" })[c]!);

/** Markdown → what's worth saying (the add-on's forSpeech, cut down): code and links aren't read out. */
export function forSpeech(md: string) {
  return md
    .replace(/```[\s\S]*?```/g, "（代码见屏幕）")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^#+\s*/gm, "")
    .replace(/[*_>|]/g, "")
    .replace(/\/[\w./-]{20,}/g, "（路径）")
    .trim();
}

async function synthesize(text: string): Promise<Uint8Array> {
  const s = await voiceSettings();
  if ((s.ttsProvider ?? "doubao") === "azure") {
    const voice = (s.azureVoice === "custom" ? s.azureVoiceName : s.azureVoice) || "zh-CN-XiaoxiaoMultilingualNeural";
    const rate = s.azureRate ?? 0;
    const lang = voice.split("-").slice(0, 2).join("-");
    const ssml =
      `<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xml:lang="${xml(lang)}">` +
      `<voice name="${xml(voice)}"><prosody rate="${rate >= 0 ? "+" : ""}${rate}%">${xml(text)}</prosody></voice></speak>`;
    const r = await fetch(`https://${s.azureRegion}.tts.speech.microsoft.com/cognitiveservices/v1`, {
      method: "POST",
      headers: { "Ocp-Apim-Subscription-Key": s.azureKey, "Content-Type": "application/ssml+xml", "X-Microsoft-OutputFormat": "audio-24khz-96kbitrate-mono-mp3" },
      body: ssml,
    });
    if (!r.ok) throw new Error(`Azure ${r.status}`);
    return new Uint8Array(await r.arrayBuffer());
  }
  const headers: Record<string, string> = { "X-Api-Resource-Id": "seed-tts-2.0", "X-Api-Request-Id": crypto.randomUUID(), "Content-Type": "application/json" };
  if (s.doubaoAppId) Object.assign(headers, { "X-Api-App-Id": s.doubaoAppId, "X-Api-Access-Key": s.doubaoKey });
  else headers["X-Api-Key"] = s.doubaoKey;
  const r = await fetch("https://openspeech.bytedance.com/api/v3/tts/unidirectional", {
    method: "POST",
    headers,
    body: JSON.stringify({
      user: { uid: "gerdr" },
      req_params: { text, speaker: s.doubaoVoice || "zh_female_xiaohe_uranus_bigtts", audio_params: { format: "mp3", sample_rate: 24000, speech_rate: s.doubaoRate ?? 0 } },
    }),
  });
  // Concatenated JSON objects, each a piece of base64 MP3
  const t = await r.text();
  const parts: Buffer[] = [];
  for (const m of t.matchAll(/"data"\s*:\s*"([^"]+)"/g)) parts.push(Buffer.from(m[1], "base64"));
  if (!parts.length) throw new Error(`豆包 ${r.status}: ${t.slice(0, 160)}`);
  return new Uint8Array(Buffer.concat(parts));
}

export const speaking = signal(false);
let reading = 0;

export function stopSpeaking() {
  reading++;
  host.call("audio.stop").catch(() => {});
  speaking.set(false);
}

// The host plays it: a plugin's view has no filesystem for AudioPlayer, which takes a file or URL, not bytes
async function playBytes(mp3: Uint8Array) {
  await host.call("audio.play", Buffer.from(mp3).toString("base64"));
}

/** Reads `text` paragraph by paragraph, synthesizing the next while one plays. */
export async function speak(text: string) {
  stopSpeaking();
  const mine = ++reading;
  speaking.set(true);
  const paras = forSpeech(text)
    .split(/\n\s*\n/)
    .map((p) => p.replace(/\s+/g, " ").trim())
    .filter(Boolean);
  try {
    let next = paras.length ? synthesize(paras[0]) : null;
    for (let i = 0; i < paras.length && mine === reading; i++) {
      const t0 = Date.now();
      const mp3 = await next!;
      console.log(`tts: piece ${i + 1}/${paras.length}, ${mp3.length} bytes, waited ${Date.now() - t0}ms`);
      next = i + 1 < paras.length ? synthesize(paras[i + 1]) : null;
      if (mine !== reading) break;
      await playBytes(mp3);
    }
  } catch (e: any) {
    console.log(`tts error: ${e?.code ?? ""} ${e?.message ?? e}`);
    throw e;
  } finally {
    if (mine === reading) speaking.set(false);
  }
}

// ---------- the composer's buttons ----------

export function MicButton(props: { base: () => string; setText: (t: string) => void; hints: () => string[]; onError: (e: string) => void }) {
  const state = signal<"idle" | "opening" | "listening" | "finishing">("idle");
  let d: Dictation | null = null;
  let base = "";
  let released = false;
  async function down() {
    console.log(`mic: down (${state()})`);
    if (state() !== "idle") return;
    released = false;
    stopSpeaking();
    state.set("opening");
    base = props.base();
    const sep = base && !/\s$/.test(base) ? " " : "";
    try {
      d = await dictate(props.hints(), (t) => props.setText(base + sep + t), props.onError);
      state.set("listening");
      if (released) up(); // let go before the mic opened
    } catch (e: any) {
      console.log(`mic: error ${e?.code ?? ""} ${e?.message ?? e}`);
      props.onError(String(e?.message ?? e));
      state.set("idle");
    }
  }
  async function up() {
    console.log(`mic: up (${state()})`);
    released = true;
    if (state() !== "listening" || !d) return;
    state.set("finishing");
    await d.stop();
    d = null;
    state.set("idle");
  }
  const color = computed(() => (state() === "listening" ? "#ef4444" : state() === "idle" ? "#a1a1aa" : "#f59e0b"));
  return (
    <Button onPressDown={down} onPressUp={up} class="rounded-full items-center justify-center active:opacity-70" style={{ width: 30, height: 30, backgroundColor: state() === "listening" ? "rgba(239,68,68,0.15)" : "transparent" }}>
      <Text class="text-[15px]" style={{ color: color() }}>
        {state() === "listening" ? "●" : "🎤"}
      </Text>
    </Button>
  );
}

export function SpeakButton(props: { text: () => string; onError: (e: string) => void }) {
  return (
    <Button
      onPress={() => (speaking() ? stopSpeaking() : speak(props.text()).catch((e) => props.onError(String(e?.message ?? e))))}
      class="rounded-full items-center justify-center active:opacity-70"
      style={{ width: 30, height: 30 }}
    >
      <Text class="text-[15px]" style={{ color: speaking() ? "#60a5fa" : "#a1a1aa" }}>
        {speaking() ? "■" : "🔊"}
      </Text>
    </Button>
  );
}
