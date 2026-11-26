import { useState } from "react";

// LoadingScreen: the "connecting" screen, drawn exactly like the page's own loading screen (loadingHTML in
// internal/uisite/html.go) that it replaces: same box, spinner, font and colours (system colours, since that one shows
// before any theme), so mounting only swaps the line of text. The spinner's phase follows the page clock like that
// one's, so it carries on at the same angle instead of restarting. Keep the two in step.
//
// inline: the same look filling only the box it's in (a panel page still loading), over that box's own background.
export function LoadingScreen({ text, inline }: { text: string; inline?: boolean }) {
  const [delay] = useState(() => -(performance.now() % 800));
  const fg = inline ? "var(--foreground)" : "CanvasText";
  return (
    <div
      style={{
        position: inline ? "absolute" : "fixed",
        inset: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 12,
        font: "13px/1.5 system-ui,-apple-system,sans-serif",
        color: fg,
        background: inline ? "var(--background)" : "Canvas",
      }}
    >
      <style>{"@keyframes __gerdr_spin{to{transform:rotate(360deg)}}"}</style>
      <div
        style={{
          width: 22,
          height: 22,
          boxSizing: "border-box",
          borderRadius: "50%",
          border: `2px solid color-mix(in srgb,${fg} 15%,transparent)`,
          borderTopColor: `color-mix(in srgb,${fg} 60%,transparent)`,
          animation: `__gerdr_spin .8s linear ${delay}ms infinite`,
        }}
      />
      <div style={{ opacity: 0.6 }}>{text}</div>
    </div>
  );
}
