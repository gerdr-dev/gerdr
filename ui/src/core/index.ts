/**
 * @gerdr/core: what gerdr's page gives the UIs mounted into it.
 *
 * A project's Work UI isn't a frame: it is compiled into gerdr's own page (internal/uisite/mounts.go) and drawn as a
 * second React root, so `import … from "@gerdr/core"` in it is this file, the same module instances gerdr runs: one
 * connection, one session store, the real chat. Each mounted page gets its key as __GERDR_PROJECT__. A panel doesn't
 * reach into Code (no openInCode): the conversations it starts are its own, and come back to it (onShowSession).
 *
 * Keep it small and stable: every Work UI written against it breaks when it changes. The types the Work's agent sees
 * are internal/work/builtin/base/ui/src/gerdr-core.d.ts: change both together.
 */
export { SessionChat } from "./SessionChat";
export { useSession } from "./sessions";
export { onShowSession } from "@/lib/panelRoute";
export { useDark } from "./theme";
export { askSecret } from "@/lib/workSecrets";
export { registerCard, type CardProps } from "@/lib/cards";
export { BuiltinCard as DefaultCard, type Card } from "@/components/chat/ToolCard";
export { toast } from "sonner";
