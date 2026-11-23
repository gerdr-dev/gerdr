/**
 * Conversations a page draws itself with SessionChat (a board's drawer): Gerdr's layout doesn't know them, so the
 * inbox asks here whether one is on screen before popping a card for it. Mounted isn't enough: a panel stays mounted,
 * invisible, while another panel or Code is shown (WorkView keeps them), so it's the element's own visibility.
 * Not its opacity: a drawer fading in starts at 0, the moment the inbox looks.
 */
const chats = new Map<HTMLElement, string>();

export function addShownChat(el: HTMLElement, id: string) {
  chats.set(el, id);
  return () => void chats.delete(el);
}

export function chatOnScreen(id: string) {
  for (const [el, sid] of chats) if (sid === id && el.isConnected && el.checkVisibility({ visibilityProperty: true })) return true;
  return false;
}
