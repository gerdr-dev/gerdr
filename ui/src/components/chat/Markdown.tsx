import { useContext } from "react";
import { defaultComponents, defaultRehypePlugins, type Components } from "streamdown";
import { MessageResponse } from "@/components/ai-elements/message";
import { openPath } from "@/lib/actions";
import { localHref, rehypePaths } from "@/lib/paths";
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

// 图片语法引用的图 / 视频 / 音频 / PDF（ChatMedia）；连着几张图排成网格
function Img(props: any) {
  return <ChatMedia src={String(props.src ?? "")} alt={props.alt} />;
}

function P({ node, children, ...rest }: any) {
  const group = imageGroup(node);
  if (group) return <MediaGrid items={group} />;
  return <p {...rest}>{children}</p>;
}

// react-markdown 默认会把 file:// 这类地址清掉；本机路径原样留给 ChatMedia（它只拿来拼 /api/fs/raw 的参数）
const urlTransform = (url: string) => (localPath(url) !== null || /^https?:|^mailto:|^#/i.test(url) ? url : "");

const components: Components = { a: A, img: Img, p: P };

export function Markdown({ children }: { children: string }) {
  return (
    <MessageResponse rehypePlugins={rehypePlugins} components={components} urlTransform={urlTransform}>
      {children}
    </MessageResponse>
  );
}
