import { useContext, useMemo } from "react";
import { ChatView as Chat } from "@gerdr/chat";
import { useCommandSet } from "@/lib/commandSets";
import type { SessionSummary } from "@/lib/types";
import { TabActive } from "../Panes";

/**
 * The chat package's ChatView, told whether this gerdr tab is showing (scroll positions are only recorded then), with
 * the session's slash commands filled in: the server sends them as a key, fetched only for sessions on screen.
 */
export function ChatView(props: { session: SessionSummary; paneId: string; focused: boolean; sub?: string }) {
  const commands = useCommandSet(props.session.commandsKey);
  const session = useMemo(() => (commands ? { ...props.session, commands } : props.session), [props.session, commands]);
  return <Chat {...props} session={session} active={useContext(TabActive)} />;
}
