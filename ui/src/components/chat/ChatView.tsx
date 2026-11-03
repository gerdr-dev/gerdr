import { useContext, useMemo } from "react";
import { ChatView as Chat, type GPart } from "@gerdr/chat";
import { useCommandSet } from "@/lib/commandSets";
import type { SessionSummary } from "@/lib/types";
import { TabActive } from "../Panes";

/**
 * The chat package's ChatView, told whether this gerdr tab is showing (scroll positions are only recorded then), with
 * the session's slash commands filled in: the server sends them as a key, fetched only for sessions on screen, and its
 * cwd set to where the agent works now.
 */
export function ChatView(props: {
  session: SessionSummary;
  paneId: string;
  focused: boolean;
  sub?: string;
  /** Scroll to a part and flash it (the package's ChatView focus): the Work drawer jumping to a card. */
  focus?: { match: (part: GPart) => boolean; seq: number };
}) {
  const commands = useCommandSet(props.session.commandsKey);
  // The chat's cwd is where the agent works: relative paths it prints, `!` commands, the @ file search. That is the
  // workdir once the agent has moved itself (Claude's EnterWorktree), not the directory the session was started in.
  const session = useMemo(() => {
    const s = props.session;
    return commands || s.workdir ? { ...s, ...(commands ? { commands } : {}), cwd: s.workdir ?? s.cwd } : s;
  }, [props.session, commands]);
  return <Chat {...props} session={session} active={useContext(TabActive)} />;
}
