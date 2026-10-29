import { Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";

const ARMED_MS = 3000;

// 原地二次确认的删除按钮：点一下变成红色的「确认删除」，再点才删；3 秒没点就退回去，按钮底下一条进度条倒数（用户要的）。
// 只按时间退，鼠标移开不退：一开始移开就退，手稍微一偏按钮就没了（用户报的）。
// 不弹窗：删一个服务商 / Agent 这种事弹个框太重，但一下就删又容易误点（用户要的：删服务商要有原地的二次确认）。
export function ConfirmDelete({ onConfirm, title = "删除", label = "确认删除" }: { onConfirm: () => void; title?: string; label?: string }) {
  const [armed, setArmed] = useState(false);
  const bar = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), ARMED_MS);
    // 进度条用 Web Animations 缩：界面的 Tailwind 是浏览器里现编的，自定义 keyframes 写不进去
    const a = bar.current?.animate([{ transform: "scaleX(1)" }, { transform: "scaleX(0)" }], { duration: ARMED_MS, easing: "linear", fill: "forwards" });
    return () => {
      clearTimeout(t);
      a?.cancel();
    };
  }, [armed]);
  if (armed)
    return (
      // key 不同：和下面那个图标按钮不是同一个元素 —— 复用的话 Button 自带的 transition-all 会把字色从白慢慢过渡成红（用户报的）；
      // 红色写成内联样式：Tailwind 是浏览器里现编的，页面上头一回出现的 text-bad 要晚一拍才生成，那一拍也是白的
      <Button
        key="armed"
        size="sm"
        variant="ghost"
        className="relative h-7 shrink-0 overflow-hidden px-2 text-[12px] transition-none hover:bg-bad/10"
        style={{ color: "var(--bad)" }}
        onClick={() => {
          setArmed(false);
          onConfirm();
        }}
      >
        <Trash2 className="size-3.5" />
        {label}
        <span ref={bar} className="absolute inset-x-0 bottom-0 h-0.5 origin-left" style={{ background: "var(--bad)" }} />
      </Button>
    );
  return (
    <Button key="idle" size="icon" variant="ghost" className="size-7 shrink-0 transition-none" title={title} onClick={() => setArmed(true)}>
      <Trash2 className="size-3.5" />
    </Button>
  );
}
