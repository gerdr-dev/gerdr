# @gerdr/chat

Gerdr's conversation UI for coding agents, as a React component library: the message list (virtualized, sticks to the
bottom while output streams), Markdown with code / images / videos / local paths, tool call cards with diffs, permission
and question cards, plans, background tasks, retry notices, stop, interjections (queued / delivered / withdraw / send
now), rewind (on every user message, with confirmation, putting the message back into the composer), `/clear`, and the
composer (images and videos, slash commands, @-files, ↑ history, Esc / Ctrl+C).

It draws an **event log** and calls back into its **host** for everything else. The log comes from the Go package
`github.com/gerdr-dev/gerdr/chatlog` (format: `chatlog/EVENTS.md` in the Gerdr repo), which records an ACP session
driven by any of Gerdr's agents (`agents/native`, `agents/claude`, `agents/codex`). Gerdr's own UI uses this package
from source; other apps install it from npm.

## Install

```sh
npm i @gerdr/chat react react-dom sonner
```

React 19, and `sonner` for toasts (render one `<Toaster />` in your app).

```ts
import "@gerdr/chat/style.css"; // the components' utilities + the chat's own rules (no global reset)
import "@gerdr/chat/tokens.css"; // optional: gerdr's light/dark theme; or define the variables below yourself
```

## Use

```tsx
import { ChatProvider, ChatView, useEventsTranscript, type ChatHost, type ChatSession, type HubEvent } from "@gerdr/chat";

function Chat({ session, events }: { session: ChatSession; events: HubEvent[] }) {
  const host: ChatHost = {
    useTranscript: () => useEventsTranscript(events), // events: append as they arrive, replace on a rewrite
    send: (id, content, o) => api.send(id, content, o?.now),
    stop: (id) => api.stop(id), // → { content } back into the composer
    withdraw: (id, msg, now) => api.withdraw(id, msg, now),
    rewind: (id, msg) => api.rewind(id, msg), // → { content }
    answer: (id, req, response) => api.answer(id, req, response),
    fileURL: (path) => `/files?path=${encodeURIComponent(path)}`,
    codeBlocks: { chart: (code) => <MyChart spec={code} /> },
    renderTool: (part) => (part.toolName === "request_user_input" ? <MyForm part={part} /> : undefined),
    onLink: (href) => href.startsWith("/?view=") && (navigate(href), true),
    composerTop: () => <ContextChips />,
    composerBar: () => <ModelMenu />,
  };
  return (
    <ChatProvider host={host} locale="en">
      <ChatView session={session} paneId="main" focused />
    </ChatProvider>
  );
}
```

A runnable version with a fake agent is in `example/` (`node build.mjs` in the package, then in `example/`, and open
`example/dist/index.html` over HTTP).

- `ChatView({ session, paneId, focused, sub?, active? })`: `session` is a `ChatSession` (status, modes / config
  options, commands, tasks, context size — `chatlog.State` has the same fields), `paneId` names this composer (drafts,
  scroll position), `focused` lets it take the keyboard, `sub` shows a sub-agent's read-only conversation, `active=false`
  while hidden.
- `ChatProvider({ host, locale, messages })`: one near the root of the page. It also mounts the image viewer and the
  tooltip context.
- The host contract is `ChatHost` in `src/host.tsx`; every member is documented there. Required: `useTranscript`,
  `send`, `stop`, `answer`, `fileURL`. Everything else is optional and turns its feature off when missing (no rewind
  buttons without `rewind`, no "stop task" without `stopTask`, no `!` command mode without `shell`, no video upload
  without `upload`…).
- Slots: `composerTop`, `composerBar`, `composerOverlay`; `renderTool`, `codeBlocks`, `codeActions`, `onLink`.
  Composer hooks (`host.composer`): a draft store, `localCommands`, `onSendError`, `onExit`, `useDefaults`, `useExtras`.

## Text

All user-visible text is in `src/messages.ts`, in `zh` and `en`. `ChatProvider locale="en"` picks one; `messages={{
"composer.idle": "Ask anything…" }}` overrides single ones. Texts written by the server (the retry notice) are the
server's.

## CSS variables the host provides

Colors (any CSS color; `.dark` on an ancestor switches to the dark set): `--background --foreground --card
--card-foreground --popover --popover-foreground --primary --primary-foreground --secondary --secondary-foreground
--muted --muted-foreground --accent --accent-foreground --destructive --border --input --ring --brand --ok --warn --bad`,
and `--radius` (lg; sm / md / xl are derived). `tokens.css` has Gerdr's values to start from.

An app on Tailwind 4 can map these onto its own theme (`--brand: var(--color-primary)`…). The components only use
classes compiled into `style.css`, so the app's Tailwind doesn't need to scan the package.

## Developing

The source is Gerdr's live UI: Gerdr serves `src/` unbuilt (a `workspace:*` dependency in `ui/package.json`, resolved by
`internal/uisite`), so edits show up in Gerdr immediately. Third-party imports must also be listed in `ui/package.json`
(same versions). No CSS imports in the code: styles go in `src/style.css`. `node build.mjs` builds `dist/` for npm.
