import * as React from "react";

/**
 * Popups whose content mounts on the first open and then stays: a closed Radix tooltip / menu still mounts its portal
 * and presence under the root, and a chat mounts many of them it never opens (the composer's buttons, rows' menus),
 * which added to every chat's first mount (tools/scrolltest/tabrenders.mjs --first). Once opened the content stays
 * mounted, so the close animation and anything kept in it work as before. The root and trigger always mount: the
 * trigger is what opens it.
 *
 * `useOpened` goes in the root wrapper: it wraps onOpenChange to notice the first open (also a controlled `open` /
 * `defaultOpen` already true) and gives the value its context provider hands to the content wrapper, which renders
 * nothing until then. One context per kind of popup, so a tooltip inside a menu's content doesn't read the menu's.
 */
export function useOpened(open: boolean | undefined, defaultOpen: boolean | undefined, onOpenChange: ((o: boolean) => void) | undefined) {
  const [opened, setOpened] = React.useState(!!(open || defaultOpen));
  if (open && !opened) setOpened(true);
  const change = React.useCallback(
    (o: boolean) => {
      if (o) setOpened(true);
      onOpenChange?.(o);
    },
    [onOpenChange],
  );
  return { opened, onOpenChange: change };
}
