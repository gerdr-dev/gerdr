# Chrome Web Store listing

Modelled on the Annulo extension's listing, which passed review (same native-messaging + `chrome.debugger` design,
same limits). Gerdr asks for two permissions Annulo doesn't: `tabGroups` and `scripting` (justified below).

Upload the zip from `scripts/pack.sh` (`dist/gerdr-extension-<version>.zip`, the manifest without `key`) in the
developer console (https://chrome.google.com/webstore/devconsole) and paste the sections below. Listing languages:
English as the default, plus Chinese (Simplified). Images: `store/render.sh` renders them into `store/out/`.

## Store listing

**Name**: Gerdr (from the manifest)

**Category**: Developer Tools

**Summary** (the manifest's `description`, filled in automatically, max 132 characters):
- en: Lets Gerdr's coding agents on this computer work on the websites you allow, in tabs they open themselves.
- zh_CN: 让这台电脑上 Gerdr 里的 agent 在你允许的网站上替你操作网页，只用它们自己打开的标签页。

**Description (English)**:

```
Gerdr is a free, open-source desktop app for coding agents (Claude Code, Codex) that run on your own computer.

Sometimes an agent needs a website where you are already signed in: checking a change in your company's admin, reading a dashboard, looking at an issue. This extension lets the agents in Gerdr do that in your own Chrome, with your existing sign-in, instead of asking you to sign in again in a separate browser.

How it stays limited:
• It only works with the Gerdr app running on the same computer. It talks to it through Chrome's native messaging; nothing goes through our servers.
• Gerdr asks before it uses a website for the first time. You allow sites one by one, and you can see and remove them from the toolbar menu at any time.
• It only operates tabs it opened itself. They sit in a tab group named "Gerdr" and the page gets a blue frame, so you always see which tabs an agent is using. It never touches your other tabs.
• If a page leaves the sites you allowed, the extension stops controlling it.
• It never reads or changes your cookies, and never reads data from other sites.

Several Chrome profiles? Give each a name in the toolbar menu ("Work", "Personal") and tell the agent which one to use.

Requires the Gerdr desktop app: https://gerdr.dev
Source code: https://github.com/gerdr-dev/gerdr
```

**Description (中文)**:

```
Gerdr 是一个免费开源的桌面应用，用来管理在你自己电脑上运行的 coding agent（Claude Code、Codex）。

有时 agent 要用到你已经登录的网站：在公司后台确认刚改的功能、看一眼数据面板、查一个 issue。装了这个插件，Gerdr 里的 agent 就能直接在你的 Chrome 里、用你已有的登录完成这些操作，不用在另一个浏览器里重新登录。

它能做的事是受限的：
• 只和同一台电脑上运行的 Gerdr 配合，通过 Chrome 的本机通信连接，不经过我们的服务器。
• 第一次要用某个网站时，Gerdr 会先问你。网站一个一个授权，随时可以在工具栏菜单里查看、移除。
• 只操作它自己打开的标签页。这些标签页归在名为「Gerdr」的标签组里，页面带蓝色边框，哪些标签页在被 agent 使用一眼就能看到。不碰你的其他标签页。
• 页面离开你允许的网站，插件就不再控制它。
• 不读取、不修改你的 cookie，不读取其他网站的数据。

有几个 Chrome 用户？在工具栏菜单里给每个起个名字（「工作」「个人」），告诉 agent 用哪个就行。

需要安装 Gerdr 桌面应用：https://gerdr.dev
源代码：https://github.com/gerdr-dev/gerdr
```

**Images**:
- Icon: `extension/icons/icon-128.png`
- Screenshots (1280×800): `store/out/en-1..3.png` for English, `store/out/zh-1..3.png` for Chinese
- Small promo tile (440×280): `store/out/en-tile.png` / `zh-tile.png`

**Website**: https://gerdr.dev
**Support**: https://github.com/gerdr-dev/gerdr/issues

## Privacy practices

**Single purpose**:

```
Let the Gerdr desktop app on the same computer operate web pages in the user's own Chrome for the user's coding agents, only on websites the user has allowed and only in tabs the extension opens itself.
```

**Permission justification**:

| Permission | Justification |
|---|---|
| debugger | Gerdr operates pages for the user's coding agents (click, type, read page content, take a screenshot) through the Chrome DevTools Protocol. The extension only attaches to tabs it opened itself, only while they are on sites the user allowed, and forwards only an allow-list of commands; cookie, storage and browser-wide commands are always refused. |
| nativeMessaging | The only way the extension talks to the Gerdr desktop app on the same computer (native host com.gerdr.browser). No remote server is involved. |
| tabs | To open, find, focus and close the extension's own tabs and windows, and to stop controlling a tab when it navigates away from the allowed sites. |
| tabGroups | To put the tabs the extension opened into a tab group named "Gerdr", so the user can always see which tabs are being operated. |
| scripting | When the extension stops controlling a tab, to remove the frame and cursor it drew on the page (marking the tab as operated by Gerdr). Only runs on tabs the extension opened, on sites the user allowed. |
| storage | To keep a random installation ID (so Gerdr can tell several Chrome profiles apart), the name the user gives this profile, and which tabs the extension opened, across service worker restarts. |
| alarms | To reconnect to the Gerdr desktop app periodically, since Chrome can stop the service worker. |
| Host permissions (optional `https://*/*`, `http://*/*`) | Optional and requested one site at a time: the first time Gerdr needs a website, the user is asked to allow that site only. The extension refuses to operate any site the user has not allowed. Users can remove sites from the toolbar menu. |

**Remote code**: No, I am not using remote code. (The page frame / cursor script is `overlay.js`, shipped in the
package.)

**Data usage**: tick "Website content" only (the extension passes the content of pages on the sites the user allowed
to the Gerdr app on the same computer). Tick all three declarations:
- I do not sell or transfer user data to third parties, outside of the approved use cases
- I do not use or transfer user data for purposes that are unrelated to my item's single purpose
- I do not use or transfer user data to determine creditworthiness or for lending purposes

**Privacy policy**: https://gerdr.dev/privacy (its "Gerdr browser extension" section)

## After it is listed

The store id is `hkooiloghnflaffpkfapfodomgkdpnff` (different from the development id, given once the draft was uploaded):
1. ~~add it to `ExtensionIDs` in `internal/chromeext/bridge.go` (the native host manifest's `allowed_origins`) and ship
   a Gerdr release; until then the store build can't connect~~ (added 2026-11-19);
2. ~~point the install instructions at the store page~~ (done 2026-11-19, before approval: Settings → Browser in the
   UI, docs/guide/usage.md, the not-connected errors and the built-in skill; the link 404s until the item is approved).
