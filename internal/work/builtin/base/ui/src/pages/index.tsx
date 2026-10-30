import { Sparkles } from "lucide-react";
import { ask, project } from "../lib/gerdr";

// A new project's starting page. Everything here is this project's own code (its Work directory's ui/):
// ask the agent on the right for the pages you want, or edit them yourself.

const IDEAS = [
  "做一个待办清单页面：能加、勾掉、删，数据存在本机函数里",
  "做一个看板页面：用一个 MCP 拉数据，按周画成图",
  "把左边菜单改成我这个项目要的几块，先给我列个方案",
];

export default function Home() {
  return (
    <div className="mx-auto max-w-2xl space-y-6 p-8 max-md:p-4">
      <div className="space-y-2">
        <div className="text-[20px] font-semibold">「{project}」的 Work</div>
        <p className="text-[13px] leading-6 text-muted-foreground">
          这里是这个项目自己的页面：左边的菜单和每个页面都是代码，放在这个项目的 Work 目录里。想要什么样的页面，跟右边的助手说就行，它会直接改，保存就生效。
        </p>
      </div>
      <div className="space-y-2">
        <div className="text-[12px] font-medium text-muted-foreground">可以这样开头</div>
        {IDEAS.map((t) => (
          <button
            key={t}
            className="flex w-full items-center gap-2.5 rounded-lg border bg-card px-3.5 py-2.5 text-left text-[13px] hover:bg-accent/60"
            onClick={() => ask(t)}
          >
            <Sparkles className="size-3.5 shrink-0 text-brand" />
            {t}
          </button>
        ))}
      </div>
    </div>
  );
}
