// 提问卡（AskCard）的键盘操作：焦点平时在输入框里，问题弹出来时不去抢它（正在打字的人会被打断），
// 而是输入框空着时把 ↑↓ / 数字 / 回车 / 空格 / ←→ 转给这个会话正在等回答的那张卡 —— 和 Claude Code 终端里一样直接按键答题；
// 打字照常进输入框，就是「不选，直接回复」。卡片挂载时登记，卸载（滚出虚拟列表、答完）时注销。

type Handler = (e: KeyboardEvent) => boolean;

const handlers = new Map<string, Handler>();

export function registerAsk(sessionId: string, fn: Handler) {
  handlers.set(sessionId, fn);
  return () => {
    if (handlers.get(sessionId) === fn) handlers.delete(sessionId);
  };
}

/** 交给这个会话的提问卡处理；它认了这个键（调用方该 preventDefault）就返回 true。 */
export function askKey(sessionId: string, e: KeyboardEvent): boolean {
  return handlers.get(sessionId)?.(e) ?? false;
}
