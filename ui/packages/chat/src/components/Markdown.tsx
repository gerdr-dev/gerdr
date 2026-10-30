import { useContext, useSyncExternalStore } from "react";
import { defaultComponents, defaultRehypePlugins, defaultRemarkPlugins, type Components, type StreamdownProps } from "streamdown";
import { MessageResponse } from "../ai/message";
import { chatHost, useChatHost } from "../host";
import { bareFileName, fileExists, localHref, onFileExists, rehypePaths } from "../paths";
import { ChatMedia, imageGroup, localPath, MediaGrid, rehypeImageGroups } from "./ChatMedia";
import { SessionContext } from "./sessionContext";

/**
 * 对话里的 Markdown：在 streamdown 默认那套之外，把本地路径变成可点的（点了原地打开，见 FileViewer）。
 *
 * 插件和 components 都放模块级：streamdown 拿 components 里的函数当组件类型，渲染里现建的话每次都是「换了个组件」，
 * 整棵子树卸掉重挂（代码块的横向滚动、选中的字全丢）。cwd 走 context，组件身份才能不变。
 * 我们的插件排在默认那几个后面：harden / sanitize 跑完了才加 data-gerdr-path，不会被它们清掉。
 */
const rehypePlugins = [...Object.values(defaultRehypePlugins), rehypePaths, rehypeImageGroups];

// GFM by default also reads a pair of single tildes as strikethrough, so a sentence naming two home paths
// (`~/.claude.json、~/.codex/config.toml`) came out crossed through. Only `~~text~~` strikes now.
const remarkPlugins = Object.values({
  ...defaultRemarkPlugins,
  gfm: [(defaultRemarkPlugins.gfm as [any, object])[0], { singleTilde: false }],
}) as StreamdownProps["remarkPlugins"];

const DefaultA = (defaultComponents as Record<string, any>).a ?? "a";

const pathLink = "cursor-pointer text-brand underline decoration-brand/40 underline-offset-2 hover:decoration-brand";

function A(props: any) {
  const session = useContext(SessionContext);
  const host = useChatHost();
  const path: string | undefined = props["data-gerdr-path"] ?? localHref(props.href);
  if (!path || !host.openPath) {
    if (!host.onLink || !props.href) return <DefaultA {...props} />;
    // The host may take a link over (an app page link opens in its own view instead of a new tab)
    return (
      <span className="contents" onClickCapture={(e) => host.onLink!(props.href, e.nativeEvent) && (e.preventDefault(), e.stopPropagation())}>
        <DefaultA {...props} />
      </span>
    );
  }
  return (
    <a
      href="#"
      title={path}
      className={pathLink}
      onClick={(e) => {
        e.preventDefault();
        host.openPath?.(path, session?.cwd);
      }}
    >
      {props.children}
    </a>
  );
}

// 图片语法引用的图 / 视频 / 音频 / PDF（ChatMedia）；连着几张图排成网格
function Img(props: any) {
  return <ChatMedia src={String(props.src ?? "")} alt={props.alt} />;
}

function P({ node, children, ...rest }: any) {
  const group = imageGroup(node);
  if (group) return <MediaGrid items={group} />;
  return <p {...rest}>{children}</p>;
}

const DefaultCode = (defaultComponents as Record<string, any>).code ?? "code";

const hastText = (n: any): string => (n?.type === "text" ? n.value : (n?.children ?? []).map(hastText).join(""));

/**
 * `! 命令` 里的命令（acpx.ShellHint 教 agent 这么写）。两条都要满足，不然正文里冒出不该有的「运行」（都是用户报的）：
 * - `!` 后面有空格：Claude Code 原话和我们的提示都是 `! <command>`；代码里的取反 `!Se()`、`!opts.isInteractive()` 从来不带空格。
 *   代价是 agent 写成 `!ls` 时不出按钮（少见；输入框里敲 `!ls` 不走这里）。
 * - 命令以像命令开头的字起头（字母、数字、路径、引号、$）：说起这个功能时写的「`! 命令`」「`! <command>`」不算。
 */
function bangCommand(text: string): string | null {
  const m = /^!\s+([\w./~$"'(-][\s\S]*)$/.exec(text.trim());
  return m ? m[1].trim() : null;
}

// 行内代码：`! 命令` 旁边画「运行」。字从 hast 节点里取：路径插件可能把里面改成了链接
// A bare file name (`README.md:45`) becomes a link once the server confirms it's in the session directory.
function InlineCode(props: any) {
  const session = useContext(SessionContext);
  const text = hastText(props.node);
  const cmd = bangCommand(text);
  const name = session?.cwd && chatHost().openPath ? bareFileName(text) : undefined;
  const found = useSyncExternalStore(onFileExists, () => (name && session?.cwd ? fileExists(name, session.cwd) : undefined));
  const code = <DefaultCode {...props} />;
  if (name && found)
    return (
      <a
        href="#"
        title={name}
        className={pathLink}
        onClick={(e) => {
          e.preventDefault();
          chatHost().openPath?.(name, session?.cwd);
        }}
      >
        {code}
      </a>
    );
  const actions = cmd && session ? chatHost().codeActions?.(cmd, "bang", session) : undefined;
  if (!actions) return code;
  return (
    <>
      {code}
      <span className="ml-1 inline-flex align-middle">{actions}</span>
    </>
  );
}

/**
 * 代码块：每一行都是 `! 命令` 时（命令长、或者一次给好几条时 agent 会写成块），整块换成一张命令列表：一条命令一行，
 * 「运行」在那一行右边，平时淡着、指过去才亮（手机上没有悬停，常亮）。原来是代码块照画、下面再堆一排按钮把命令重抄一遍，
 * 同一条命令出现两次，看着乱（用户报的）。
 * 普通的 bash 代码块不加：那多半是给人看的、或者 agent 自己要跑的，点一下就跑太冒失。
 */
function CodeBlock(props: any) {
  const session = useContext(SessionContext);
  const host = useChatHost();
  const text = hastText(props.node);
  // A language the host draws itself (```chart)
  const lang = /language-([\w-]+)/.exec(String(props.className ?? props.node?.properties?.className ?? ""))?.[1];
  const custom = lang ? host.codeBlocks?.[lang] : undefined;
  if (custom) return <>{custom(text.replace(/\n$/, ""))}</>;
  const lines = text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  const cmds = lines.map(bangCommand);
  if (!session || !host.codeActions || !cmds.length || cmds.some((c) => !c)) return <DefaultCode {...props} />;
  return (
    // 外框、字号、行距都和普通代码块一家：挂上同样的 data-streamdown，index.css 里 .gerdr-md 那几条（字号 0.86em、行距 1.5、
    // 淡底、单层框）一并套上；原来自己写 text-sm + 24px 行高，比旁边的代码块大一号、行距也松（用户报的）
    <div data-streamdown="code-block" className="my-4 flex flex-col rounded-xl border border-border">
      <div data-streamdown="code-block-body" className="font-mono">
        {cmds.map((c, i) => (
          <div key={i} className="group/cmd -mx-1.5 flex min-w-0 items-start gap-2 rounded-md px-1.5 hover:bg-muted/60">
            <span className="shrink-0 font-semibold text-brand">!</span>
            {/* 长命令折行显示全：要跑的东西得让人看全了再点，截成一行加省略号的话后半截看不到 */}
            <span className="min-w-0 flex-1 whitespace-pre-wrap [overflow-wrap:anywhere]">{c}</span>
            <span className="shrink-0 font-sans opacity-40 transition-opacity group-hover/cmd:opacity-100 [@media(hover:none)]:opacity-100">
              {host.codeActions!(c!, "bang", session)}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

// react-markdown 默认会把 file:// 这类地址清掉；本机路径原样留给 ChatMedia（它只拿来拼 /api/fs/raw 的参数）
const urlTransform = (url: string) => (localPath(url) !== null || /^https?:|^mailto:|^#/i.test(url) ? url : "");

const components: Components = { a: A, img: Img, p: P, inlineCode: InlineCode, code: CodeBlock };

export function Markdown({ children }: { children: string }) {
  return (
    <MessageResponse remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} components={components} urlTransform={urlTransform}>
      {children}
    </MessageResponse>
  );
}
