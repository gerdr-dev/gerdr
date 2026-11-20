// A stand-in chat for recording nsh's fling fix (https://github.com/longbridge/nsh/pull/2642) without real content:
// an inverted List of made-up messages whose heights differ a lot (one line to a few screens), as a chat's do, with
// `estimatedItemSize` far from most of them. Each row shows its number, so a jump is easy to see.
import { mount, Text, View, List } from "nsh";

const WORDS = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua".split(" ");

// Deterministic, so both recordings show the same list
let seed = 7;
const rand = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);

type Msg = { id: number; mine: boolean; text: string };
const messages: Msg[] = Array.from({ length: 400 }, (_, i) => {
  const r = rand();
  // Mostly short, some long: 3 – 400 words
  const n = r < 0.5 ? 3 + Math.floor(rand() * 20) : r < 0.85 ? 30 + Math.floor(rand() * 80) : 150 + Math.floor(rand() * 250);
  const text = Array.from({ length: n }, () => WORDS[Math.floor(rand() * WORDS.length)]).join(" ");
  return { id: 400 - i, mine: rand() < 0.3, text };
});

function App() {
  return (
    <View style={{ flex: 1, backgroundColor: "#f4f4f5", paddingTop: "env(safe-area-inset-top)" } as any}>
      <View style={{ height: 48, justifyContent: "center", paddingHorizontal: 16, backgroundColor: "#ffffff", borderBottomWidth: 1, borderBottomColor: "#e4e4e7" }}>
        <Text style={{ fontSize: 16, fontWeight: "600", color: "#18181b" }}>Inverted List · 400 rows</Text>
      </View>
      <List each={messages} keyExtractor={(m) => String(m.id)} inverted estimatedItemSize={120} style={{ flex: 1 }}>
        {(m) => (
          <View style={{ paddingHorizontal: 12, paddingVertical: 6, alignItems: m().mine ? "flex-end" : "flex-start" }}>
            <View style={{ maxWidth: "85%", borderRadius: 16, paddingHorizontal: 14, paddingVertical: 10, backgroundColor: m().mine ? "#337bd0" : "#ffffff" }}>
              <Text style={{ fontSize: 12, fontWeight: "700", color: m().mine ? "#dbeafe" : "#71717a", marginBottom: 4 }}>#{m().id}</Text>
              <Text style={{ fontSize: 15, lineHeight: 22, color: m().mine ? "#ffffff" : "#27272a" }}>{m().text}</Text>
            </View>
          </View>
        )}
      </List>
    </View>
  );
}

mount(App);
