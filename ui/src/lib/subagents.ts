// Where "查看对话" on a subagent's card opens it. In Code: a pane split off the tab (actions/panes openSubagent). In a
// panel's chat (SessionChat, mounted in a Work page) that pane would land in a Code tab the user isn't looking at —
// it switched to Code and opened nothing useful (reported from the Superagent board on a phone). So a mounted
// SessionChat registers itself for its session, and while a panel is what's shown, the subagent opens in that chat.

type Opener = (toolCallId: string, title?: string) => void;
const openers = new Map<string, Opener[]>();

/** A mounted chat of `sessionId` takes its subagents; returns the unregister. The latest mounted wins. */
export function takeSubagents(sessionId: string, open: Opener) {
  openers.set(sessionId, [...(openers.get(sessionId) ?? []), open]);
  return () => {
    const left = (openers.get(sessionId) ?? []).filter((f) => f !== open);
    if (left.length) openers.set(sessionId, left);
    else openers.delete(sessionId);
  };
}

/** The mounted chat that takes `sessionId`'s subagents, if any. */
export function subagentOpener(sessionId: string): Opener | undefined {
  const l = openers.get(sessionId);
  return l?.[l.length - 1];
}
