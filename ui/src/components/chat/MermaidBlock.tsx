import { openLightbox } from "@gerdr/chat";
import { useEffect, useState } from "react";
import { defaultComponents, useIsCodeFenceIncomplete } from "streamdown";
import { useDark } from "@/core/theme";
import { t } from "@/lib/i18n";
import { drawnMermaid, mermaidImageURL, renderMermaid } from "@/lib/mermaid";
import { addonCodeBlock } from "@/lib/addons";
import { AddonCodeBlock } from "@/components/AddonSlot";

const SETTLE_MS = 300;

/**
 * A ```mermaid block as its diagram (lib/mermaid.ts), in replies (ChatHost.codeBlocks) and .md files (FileViewer).
 * Tap / click opens it in the image lightbox: pinch, wheel and drag to zoom, download. Until it is drawn (Mermaid
 * loading, the reply still streaming the block) and when Mermaid can't read it, the source shows as before; a bad
 * source also says why.
 */
export function MermaidBlock({ code }: { code: string }) {
  const streaming = useIsCodeFenceIncomplete();
  const dark = useDark();
  const key = `${dark}\n${code}`;
  // Drawn diagrams are kept by source in lib/mermaid.ts, not here: the conversation is a virtual list and unmounts rows
  const [out, setOut] = useState<{ key: string; svg?: string; error?: string } | null>(null);
  useEffect(() => {
    if (streaming || drawnMermaid(code, dark) !== undefined) return;
    let live = true;
    // A reply streams in a few characters at a time, and the chat doesn't tell Streamdown when it's done (no
    // isAnimating, so `streaming` above stays false): draw once the source has stopped changing for a moment, not
    // every half-written version of it (each one a flash of a broken diagram or an error)
    const timer = setTimeout(
      () =>
        renderMermaid(code, dark).then(
          (svg) => live && setOut({ key, svg }),
          (e) => live && setOut({ key, error: String(e?.message ?? e).split("\n")[0] }),
        ),
      SETTLE_MS,
    );
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [key, streaming]);
  const mine = out?.key === key ? out : null;
  // While another version is drawn (the theme switched), the last diagram stays rather than flashing the source
  const svg = drawnMermaid(code, dark) ?? mine?.svg ?? (mine?.error ? undefined : out?.svg);
  if (!svg) return <Source code={code} error={mine?.error} />;
  const zoom = (el: HTMLElement) => openLightbox([{ url: mermaidImageURL(svg, dark), alt: "mermaid" }], 0, el.querySelector("svg"));
  return (
    <div
      role="button"
      tabIndex={0}
      title={t("mermaidBlock.zoom")}
      className="my-4 flex cursor-zoom-in justify-center overflow-hidden rounded-xl border border-border p-3 [&_svg]:h-auto [&_svg]:max-w-full"
      onClick={(e) => zoom(e.currentTarget)}
      onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), zoom(e.currentTarget))}
      // Mermaid's own markup, sanitized by it (securityLevel strict): no scripts, no handlers
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}

/** The block's source, drawn like any code block (the chat's CSS styles data-streamdown="code-block"). */
function Source({ code, error }: { code: string; error?: string }) {
  return (
    <div data-streamdown="code-block" className="my-4 flex flex-col rounded-xl border border-border">
      <div data-streamdown="code-block-header" className="flex items-center gap-2 font-mono text-xs text-muted-foreground">
        <span>mermaid</span>
        {error && <span className="min-w-0 truncate font-sans text-bad">{t("mermaidBlock.failed", { error })}</span>}
      </div>
      <div data-streamdown="code-block-body">
        <pre className="overflow-x-auto font-mono">
          <code>{code}</code>
        </pre>
      </div>
    </div>
  );
}

const DefaultCode = (defaultComponents as Record<string, any>).code ?? "code";
const hastText = (n: any): string => (n?.type === "text" ? n.value : (n?.children ?? []).map(hastText).join(""));

/**
 * Streamdown's code block component with ```mermaid and add-ons' languages (g.codeBlock) drawn (FileViewer's Markdown;
 * replies go through ChatHost.codeBlocks).
 */
export function MdCode(props: any) {
  const lang = /language-([\w-]+)/.exec(String(props.className ?? props.node?.properties?.className ?? ""))?.[1];
  const reg = lang ? addonCodeBlock(lang) : undefined;
  if (reg) return <AddonCodeBlock reg={reg} lang={lang!} code={hastText(props.node).replace(/\n$/, "")} />;
  if (lang === "mermaid") return <MermaidBlock code={hastText(props.node).replace(/\n$/, "")} />;
  return <DefaultCode {...props} />;
}
