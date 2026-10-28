import { useContext } from "react";
import { defaultComponents, defaultRehypePlugins, type Components } from "streamdown";
import { MessageResponse } from "@/components/ai-elements/message";
import { openPath } from "@/lib/actions";
import { localHref, rehypePaths } from "@/lib/paths";
import { SessionContext } from "./sessionContext";

/**
 * 对话里的 Markdown：在 streamdown 默认那套之外，把本地路径变成可点的（点了原地打开，见 FileViewer）。
 *
 * 插件和 components 都放模块级：streamdown 拿 components 里的函数当组件类型，渲染里现建的话每次都是「换了个组件」，
 * 整棵子树卸掉重挂（代码块的横向滚动、选中的字全丢）。cwd 走 context，组件身份才能不变。
 * 我们的插件排在默认那几个后面：harden / sanitize 跑完了才加 data-gerdr-path，不会被它们清掉。
 */
const rehypePlugins = [...Object.values(defaultRehypePlugins), rehypePaths];

const DefaultA = (defaultComponents as Record<string, any>).a ?? "a";

function A(props: any) {
  const session = useContext(SessionContext);
  const path: string | undefined = props["data-gerdr-path"] ?? localHref(props.href);
  if (!path) return <DefaultA {...props} />;
  return (
    <a
      href="#"
      title={path}
      className="cursor-pointer text-brand underline decoration-brand/40 underline-offset-2 hover:decoration-brand"
      onClick={(e) => {
        e.preventDefault();
        openPath(path, session?.cwd);
      }}
    >
      {props.children}
    </a>
  );
}

const components: Components = { a: A };

export function Markdown({ children }: { children: string }) {
  return (
    <MessageResponse rehypePlugins={rehypePlugins} components={components}>
      {children}
    </MessageResponse>
  );
}
