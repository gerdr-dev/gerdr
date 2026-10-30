import { useContext } from "react";
import { ChatView as Chat } from "@gerdr/chat";
import type { SessionSummary } from "@/lib/types";
import { TabActive } from "../Panes";

/** The chat package's ChatView, told whether this gerdr tab is showing (scroll positions are only recorded then). */
export function ChatView(props: { session: SessionSummary; paneId: string; focused: boolean; sub?: string }) {
  return <Chat {...props} active={useContext(TabActive)} />;
}
