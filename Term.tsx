// A terminal pane: nsh's own Terminal (its emulator in Rust) on one of Gerdr's PTYs. The web UI's Terminal.tsx does
// the same with xterm.js: POST /api/terms makes it, /api/terms/{id}/ws carries it (binary frames are bytes both ways,
// text frames JSON control: {t:"resize",cols,rows}, {t:"ping"}; server/termapi.go).
import { signal, cleanup, Text, View } from "nsh";
import { Terminal } from "nsh/base";
import type { TerminalRef } from "nsh/base";
import { connect } from "websocket";
import { BASE, token, api } from "./store";

export async function createTerm(cwd?: string): Promise<string> {
  const t = await api("POST", "/api/terms", { cwd, cols: 100, rows: 30 });
  return t.id;
}

export function TermView(props: { id: string }) {
  let term: TerminalRef | undefined;
  let sock: any = null;
  let closed = false;
  const status = signal("connecting");
  const title = signal("");
  let size = { cols: 100, rows: 30 };
  const enc = new TextEncoder();
  // Output arrives as bytes cut anywhere: a streaming decoder keeps a character split across frames whole
  const dec = new TextDecoder("utf-8");

  async function run() {
    sock = await connect(`ws://${BASE}/api/terms/${encodeURIComponent(props.id)}/ws`, { headers: { Authorization: `Bearer ${token()}` } });
    status.set("");
    sock.write(JSON.stringify({ t: "resize", ...size })).catch(() => {});
    const beat = setInterval(() => sock?.write(JSON.stringify({ t: "ping" })).catch(() => {}), 20000);
    for (let m; (m = await sock.read()) !== null; ) {
      if (typeof m === "string") {
        try {
          const c = JSON.parse(m);
          if (c.t === "exit") status.set(`exited (${c.code})`);
        } catch {}
        continue;
      }
      term?.write(dec.decode(m, { stream: true }));
    }
    clearInterval(beat);
    if (!closed) status.set("disconnected");
  }
  run().catch((e) => status.set(`error: ${e?.message ?? e}`));
  cleanup(() => {
    closed = true;
    sock?.close().catch?.(() => {});
  });

  return (
    <View class="flex-1" style={{ backgroundColor: "#0b0b0c" }}>
      <Terminal
        ref={(r) => (term = r)}
        onData={({ data }) => sock?.write(enc.encode(data)).catch(() => {})}
        onResize={({ cols, rows }) => {
          size = { cols, rows };
          sock?.write(JSON.stringify({ t: "resize", cols, rows })).catch(() => {});
        }}
        onTitleChange={({ title: t }) => title.set(t)}
        fontFamily="Menlo"
        fontSize={12.5}
        lineHeight={17}
        scrollback={5000}
        cursorClass="bg-foreground/70"
        class="flex-1 px-3 py-2"
      />
      <If when={status()}>
        <Text class="absolute text-[11.5px]" style={{ right: 12, top: 8, color: "#a1a1aa" }}>
          {status()}
        </Text>
      </If>
    </View>
  );
}
