// Gerdr plugin "voice", an nsh app of its own: Gerdr's nsh UI places it in the composer's bar through a plugin slot
// (the native component `gerdr.plugin`, gerdr.worktrees/nsh-android NshPlugins.kt). It runs in its own VM with the
// capabilities its nsh.json grants: the speech services' hosts, and the host's `gerdr.*`, `mic.*`, `audio.*`.
//
// The slot's protocol:
//   in   host.on("ctx", { session, text, lastReply, settings, hints })   the main UI's state, on every change
//   out  host.call("gerdr.size", { width, height })                      the room it takes in the bar (dp)
//        host.call("gerdr.composer.set", { text })                       replaces the composer's text
//        host.call("gerdr.error", message)                                shown by the main UI
import { mount, signal, host, View } from "nsh";
import { MicButton, SpeakButton, settings } from "./voice";

type Ctx = { session: string; text: string; lastReply: string; settings: Record<string, any>; hints: string[] };
const ctx = signal<Ctx>({ session: "", text: "", lastReply: "", settings: {}, hints: [] });
host.on<Ctx>("ctx", (c) => {
  ctx.set(c);
  settings.set(c.settings ?? {});
});

const error = (e: string) => host.call("gerdr.error", e).catch(() => {});

function App() {
  // Two 30dp buttons with a gap: the slot takes this, the bar lays it out like any other control
  host.call("gerdr.size", { width: 64, height: 30 }).catch(() => {});
  return (
    <View class="flex-row items-center" style={{ gap: 4, height: 30 }}>
      <SpeakButton text={() => ctx().lastReply} onError={error} />
      <MicButton
        base={() => ctx().text}
        setText={(text) => host.call("gerdr.composer.set", { text }).catch(() => {})}
        hints={() => ctx().hints}
        onError={error}
      />
    </View>
  );
}

mount(App);
