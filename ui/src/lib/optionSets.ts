import { useEffect, useMemo, useState } from "react";
import type { ConfigOption, SessionMode } from "@gerdr/chat/types";
import { api } from "./api";
import type { SessionSummary } from "./types";

// A session's mode / option dropdowns come as a key plus the current values, not inline (hub optSets: the same lists
// in every summary were most of /api/hello and of every summary pushed while an agent streams). The lists are fetched
// here once per key, when a session is on screen, and put back into the shape the chat package draws.

type OptionSet = { modes?: SessionMode[]; config?: ConfigOption[] };

const cache = new Map<string, OptionSet>();
const inflight = new Map<string, Promise<OptionSet>>();

function load(key: string) {
  let p = inflight.get(key);
  if (!p) {
    p = api
      .optionSet(key)
      .then((o) => (cache.set(key, o), o))
      .finally(() => inflight.delete(key));
    inflight.set(key, p);
  }
  return p;
}

function useOptionSet(key: string | undefined): OptionSet | undefined {
  const [, bump] = useState(0);
  useEffect(() => {
    if (!key || cache.has(key)) return;
    let live = true;
    load(key).then(
      () => live && bump((n) => n + 1),
      () => {}, // a key the server dropped: the next summary brings a fresh one
    );
    return () => {
      live = false;
    };
  }, [key]);
  return key ? cache.get(key) : undefined;
}

/**
 * The session's modes / configOptions as the chat package wants them: from its optsKey and current values, or as
 * they came inline (a host on an older Gerdr). Undefined fields until the lists are in.
 */
export function useSessionOptions(s: SessionSummary): Pick<SessionSummary, "modes" | "configOptions"> {
  const set = useOptionSet(s.optsKey);
  // Every pushed summary is a new object: rebuild only when a value really changed, so the composer isn't redrawn
  // on every streamed chunk
  const values = JSON.stringify(s.optValues ?? {});
  return useMemo(() => {
    if (!s.optsKey || !set) return { modes: s.modes, configOptions: s.configOptions };
    const v: Record<string, string | boolean> = JSON.parse(values);
    return {
      modes: set.modes?.length ? { availableModes: set.modes, currentModeId: s.modeId ?? "" } : undefined,
      configOptions: set.config?.map((o) => ({ ...o, currentValue: String(v[o.id] ?? o.currentValue ?? "") })),
    };
  }, [set, s.optsKey, s.modeId, values, s.modes, s.configOptions]);
}
