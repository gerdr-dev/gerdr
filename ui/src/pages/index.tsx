import App from "@/App";

// 首页就是整个工作台（tab、分屏、会话都在这一页里）。要加新页面：在 src/pages/ 下新建文件，
// 比如 src/pages/usage.tsx → /usage，用 react-router 的 <Link to="/usage"> 跳过去。
export default function Index() {
  return <App />;
}
