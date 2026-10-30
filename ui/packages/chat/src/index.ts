// @gerdr/chat: gerdr's conversation UI as a component library. See ../README.md.
export { ChatView } from "./components/ChatView";
export { Composer, type ComposerHandle, MODE, flatOptions } from "./components/Composer";
export { StatusLine, fmtElapsed, fmtTokens } from "./components/StatusLine";
export { PixelLoader } from "./components/PixelLoader";
export { Markdown } from "./components/Markdown";
export { LightboxHost, openLightbox, lightboxOpen, type LightboxItem } from "./components/Lightbox";
export { SessionContext, useSession } from "./components/sessionContext";
export { ChatProvider, useChatHost, useChatSettings, chatHost } from "./host";
export type { ChatHost, ChatTranscript, ChatSettings, ComposerHooks, ComposerContext } from "./host";
export { t, setMessages, currentLocale, type Locale, type MessageKey } from "./i18n";
export { zh, en } from "./messages";
export { memoryDrafts, type DraftImage, type DraftStore } from "./drafts";
export { useEventsTranscript } from "./events";
export { Transcript, type GMessage, type GPart, type GData, type GMeta, type ToolPart, type ToolMeta, type ToolContent, type PlanEntry } from "./transcript";
export * from "./types";
