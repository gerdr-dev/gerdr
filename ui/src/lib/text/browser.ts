// The Browser pane (components/browser/, docs/dev/BROWSER-PANE.md). en is typed against zh.
export const zh = {
  "browser.back": "后退",
  "browser.forward": "前进",
  "browser.reload": "刷新",
  "browser.address": "输入网址或搜索",
  "browser.pages": "页面",
  "browser.newPage": "新页面",
  "browser.closePage": "关掉这个页面",
  "browser.loading": "正在打开…",
  "browser.empty": "项目浏览器",
  "browser.emptyHint":
    "在上面输入网址打开。这是「{project}」项目自己的浏览器：登录过的网站在这个项目里一直保持登录，agent 用 gerdr browser 打开的页面也显示在这里。",
  "browser.gone": "这个页面已经关了",
  "browser.reopen": "重新打开",
  "browser.failed": "打不开这个网页",
  "browser.retry": "重试",
  "browser.retrying": "正在重试…",
  "browser.keyboard": "键盘",
};

export const en: Record<keyof typeof zh, string> = {
  "browser.back": "Back",
  "browser.forward": "Forward",
  "browser.reload": "Reload",
  "browser.address": "Type a URL or search",
  "browser.pages": "Pages",
  "browser.newPage": "New page",
  "browser.closePage": "Close this page",
  "browser.loading": "Opening…",
  "browser.empty": "Project browser",
  "browser.emptyHint":
    "Type a URL above. This is the {project} project's own browser: sites you sign in to stay signed in for this project, and pages agents open with gerdr browser show here too.",
  "browser.gone": "This page was closed",
  "browser.reopen": "Open again",
  "browser.failed": "This page couldn't be opened",
  "browser.retry": "Try again",
  "browser.retrying": "Trying again…",
  "browser.keyboard": "Keyboard",
};
