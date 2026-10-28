/// <reference path="./gerdr.d.ts" />
// prefix+a：复制当前 tab，插到它后面；prefix+i：插到前面（tmux 的 a / i）。
// 复制的是「工位」：同一个目录、同一种 agent，开一个新会话 —— 对话本身复制不了。
export default definePlugin({
  name: "duplicate-tab",
  setup(g) {
    const dup = (where: "after" | "before") => async () => {
      const tab = g.state().activeTab;
      if (tab) await g.actions.duplicateTab(tab.id, where);
    };
    const a = g.command({ id: "duplicate-tab.after", title: "Duplicate tab (after)", key: "prefix+a", run: dup("after") });
    const b = g.command({ id: "duplicate-tab.before", title: "Duplicate tab (before)", key: "prefix+i", run: dup("before") });
    return () => { a(); b(); };
  },
});
