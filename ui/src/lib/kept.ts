import { useState } from "react";

// 组件被卸载后还要记得的小状态（工具卡片展开没、思考过程展开没……）。对话是虚拟列表，滚出屏幕的行会被卸载，
// 滚回来时照这里恢复；切 tab 时也一样。按「会话 + 东西」做 key，只在内存里（刷新页面就忘了，那没关系）。
const store = new Map<string, unknown>();

export function useKept<T>(key: string | undefined, init: T): [T, (v: T) => void] {
  const [v, setV] = useState<T>(() => (key && store.has(key) ? (store.get(key) as T) : init));
  return [
    v,
    (next: T) => {
      if (key) store.set(key, next);
      setV(next);
    },
  ];
}
