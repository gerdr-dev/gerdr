/// <reference path="./gerdr.d.ts" />
// 示例：某个会话跑完了、或者在等你批准时，如果它不在你眼前，就弹一条系统通知。
// （gerdr 自己已经会在侧栏上标红点；这个插件演示 g.on 和 g.notify 怎么用。默认关着，
//  想用就把文件名开头的下划线去掉。）
export default definePlugin({
  name: "notify-when-done",
  setup(g) {
    const off1 = g.on("turnEnd", (e) => {
      if (!e.visible) g.notify(`${e.session.title || e.session.agent} 跑完了`, { system: true });
    });
    const off2 = g.on("needsInput", (e) => {
      if (!e.visible) g.notify(`${e.session.title || e.session.agent} 在等你`, { system: true });
    });
    return () => { off1(); off2(); };
  },
});
