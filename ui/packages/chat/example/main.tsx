// A minimal host for @gerdr/chat with no gerdr behind it: the conversation is a plain event log (what chatlog.Session's
// OnEvents would push over your own SSE / WebSocket), the actions are local functions. `node build.mjs` in this
// directory bundles it into dist/ (open dist/index.html). In a real app replace the fake agent with calls to your server.
import { useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { Toaster } from "sonner";
import { ChatProvider, ChatView, useEventsTranscript, type ChatHost, type ChatSession, type ContentBlock, type HubEvent } from "../src";

let seq = 0;
const ev = (e: Omit<HubEvent, "seq" | "ts">): HubEvent => ({ seq: ++seq, ts: Date.now(), ...e });
const update = (u: object) => ev({ kind: "update", update: u });

// What a short turn looks like in the log
const initial: HubEvent[] = [
  ev({ kind: "user", messageId: "m1", content: [{ type: "text", text: "How are sales doing this month?" }] }),
  ev({ kind: "turn_start", messageId: "m1" }),
  update({ sessionUpdate: "agent_message_chunk", messageId: "a1", content: { type: "text", text: "Let me query the orders table." } }),
  update({ sessionUpdate: "tool_call", toolCallId: "t1", title: "db_query", kind: "other", status: "completed", rawInput: { sql: "select day, sum(total) from orders group by day" } }),
  update({ sessionUpdate: "agent_message_chunk", messageId: "a2", content: { type: "text", text: "Up 12% on last month:\n\n```chart\n[3, 5, 4, 7, 9]\n```\n\nDetails are on the [sales page](/?view=sales)." } }),
  ev({ kind: "turn_end", messageId: "m1", stopReason: "end_turn", durationMs: 4200, outputTokens: 310 }),
];

function App() {
  const [events, setEvents] = useState(initial);
  const [running, setRunning] = useState(false);
  const push = (...xs: HubEvent[]) => setEvents((cur) => [...cur, ...xs]);
  const session: ChatSession = { id: "s1", agent: "native", base: "native", cwd: "/tmp/demo", status: running ? "running" : "idle", context: { tokens: 42000, window: 200000 } };
  const host = useMemo<ChatHost>(
    () => ({
      useTranscript: () => useEventsTranscript(events),
      agentName: () => "Assistant",
      async send(_id, content: ContentBlock[]) {
        const id = crypto.randomUUID();
        push(ev({ kind: "user", messageId: id, content }), ev({ kind: "turn_start", messageId: id }));
        setRunning(true);
        setTimeout(() => {
          push(
            update({ sessionUpdate: "agent_message_chunk", messageId: id + "r", content: { type: "text", text: "Done." } }),
            ev({ kind: "turn_end", messageId: id, stopReason: "end_turn", durationMs: 800, outputTokens: 3 }),
          );
          setRunning(false);
        }, 800);
      },
      stop: async () => ({ content: [] }),
      // Rewind: drop the message and everything after it, hand its content back to the composer
      async rewind(_id, messageId) {
        const i = events.findIndex((e) => e.kind === "user" && e.messageId === messageId);
        const content = events[i]?.content ?? [];
        setEvents(events.slice(0, i));
        return { content };
      },
      answer: async () => {},
      fileURL: (p) => p,
      // Your own renderers: a ```chart block, a tool card, app links
      codeBlocks: { chart: (code) => <Bars values={JSON.parse(code)} /> },
      renderTool: (part) =>
        part.toolName === "db_query" || part.title === "db_query" ? (
          <div className="my-1 rounded-md border px-2 py-1 font-mono text-[12px] text-muted-foreground">SQL · {(part.input as any)?.sql}</div>
        ) : undefined,
      onLink: (href) => href.startsWith("/?view=") && (alert("open " + href), true),
      composerBar: () => <span className="px-1 text-[12px] text-muted-foreground">model ▾</span>,
      settings: { sendKey: "enter" },
    }),
    [events],
  );
  return (
    <ChatProvider host={host} locale="en">
      <div className="h-full">
        <ChatView session={session} paneId="main" focused />
      </div>
    </ChatProvider>
  );
}

function Bars({ values }: { values: number[] }) {
  const max = Math.max(...values);
  return (
    <div className="my-2 flex h-20 items-end gap-1">
      {values.map((v, i) => (
        <div key={i} className="w-6 rounded-sm bg-brand" style={{ height: `${(v / max) * 100}%` }} />
      ))}
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <>
    <App />
    <Toaster />
  </>,
);
