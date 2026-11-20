// smoothWheel side by side: the left list scrolls a mouse wheel as AppKit does (the default), the right one with
// smoothWheel, as browsers do. Same made-up messages in both.
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

function Column(props: { smooth: boolean }) {
  return (
    <View style={{ flex: 1, borderRightWidth: 1, borderRightColor: "#e4e4e7" }}>
      <View style={{ height: 40, justifyContent: "center", paddingHorizontal: 12, backgroundColor: "#ffffff", borderBottomWidth: 1, borderBottomColor: "#e4e4e7" }}>
        <Text style={{ fontSize: 14, fontWeight: "600", color: "#18181b" }}>{props.smooth ? "smoothWheel" : "default (AppKit)"}</Text>
      </View>
      <List each={messages} keyExtractor={(m) => String(m.id)} smoothWheel={props.smooth} estimatedItemSize={120} style={{ flex: 1 }}>
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

function App() {
  return (
    <View style={{ flex: 1, flexDirection: "row", backgroundColor: "#f4f4f5" }}>
      <Column smooth={false} />
      <Column smooth={true} />
    </View>
  );
}

mount(App);
