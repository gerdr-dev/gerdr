import { createContext, useContext } from "react";
import type { ChatSession } from "../types";

// 对话里深处的组件（工具调用卡片）要知道「这是哪个会话、它有哪些后台任务」，别一层层往下传。
export const SessionContext = createContext<ChatSession | null>(null);
export const useSession = () => useContext(SessionContext);
