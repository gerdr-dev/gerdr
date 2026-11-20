package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// gerdr browser: work in the user's own Chrome, signed in as they are, through Gerdr's browser extension
// (internal/chromeext, docs/dev/CHROME-EXT.md). Every action is one POST /api/browser/act; `gerdr browser mcp`
// serves the same actions as MCP tools (browsermcp.go).
func browserCmd() *cobra.Command {
	var asJSON bool
	var tab int
	cmd := &cobra.Command{
		Use:   "browser",
		Short: i18n.S("操作网页：默认在项目自己的浏览器里（界面右侧的浏览器面板），--chrome 在用户自己的 Chrome 里", "Work on web pages: in the project's own browser by default (the Browser pane beside the chat), with --chrome in the user's own Chrome"),
		Long: i18n.S(`开网页、操作网页。两种浏览器，操作完全一样：

- 项目浏览器（默认）：Gerdr 在后台跑的 Chrome，每个项目一份自己的 profile，登录过的网站在这个项目里一直保持登录。
  打开的页面显示在对话旁边的「浏览器」面板里，用户能看着你操作，也能自己点、自己输入（登录、验证码交给用户）。
- 用户自己的 Chrome（open --chrome / --profile）：用户平时用的那个，已经登好的账号都在。要装 Gerdr 浏览器插件、Chrome 开着；
  插件的限制改不了：只操作它自己开的窗口；每个网站第一次用时插件弹窗问用户「允许」（open 会一直等，最多 10 分钟），
  页面跳到没允许的网站就停止操作；读 cookie、存储这类 DevTools 命令一律不放行。

典型流程：open 开页面 → snapshot 看页面上有什么（每个可操作的东西带一个 ref，如 e12）→ click / type / fill / key / scroll 按 ref 操作
→ 再 snapshot 或 screenshot 看结果 → 用完 close。操作用真实的鼠标键盘事件，富文本输入框也认。
不加 -t 时操作最后打开的那个页面。同样的操作也有 MCP 版：gerdr browser mcp。
要登录的网站：先 open，如果页面要登录，请用户在浏览器面板里登录（在 Gerdr 里看得到），登完再继续；以后这个项目里就一直是登录的。`,
			`Open web pages and work on them. Two browsers, the same actions on both:

- The project's browser (default): a Chrome Gerdr runs in the background, with a profile per project, so a site signed
  in to once stays signed in for that project. Its pages show in the Browser pane beside the chat: the user watches
  you work and can click and type there themselves (hand them sign-ins and captchas).
- The user's own Chrome (open --chrome / --profile): the one they use every day, signed in as they already are. Needs
  the Gerdr browser extension and Chrome open. The extension's limits can't be changed: it only drives windows it
  opened; the first time a site is used it asks the user to Allow (open waits, up to 10 minutes); going to a site that
  isn't allowed stops it; cookie, storage and similar DevTools commands are never let through.

The usual loop: open a page → snapshot to see what is on it (everything you can act on gets a ref, like e12)
→ click / type / fill / key / scroll by ref → snapshot or screenshot again to see the result → close when done.
Actions are real mouse and keyboard events, which rich text editors accept. Without -t they act on the page opened
last. The same actions as MCP tools: gerdr browser mcp.
Sites that need a sign-in: open the page; if it asks to sign in, ask the user to do it in the Browser pane (they see it
in Gerdr), then carry on. The project stays signed in from then on.`),
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error { return browserStatus(asJSON) },
	}
	cmd.PersistentFlags().BoolVar(&asJSON, "json", false, i18n.S("输出 JSON", "print JSON"))
	cmd.PersistentFlags().IntVarP(&tab, "tab", "t", 0, i18n.S("哪个窗口（open 打印的 tab；默认最后打开的那个）", "which window (the tab open printed; default: the one opened last)"))

	act := func(action string, a chromeext.ActArgs) (*chromeext.ActResult, error) {
		return browserAct(tab, action, a)
	}
	show := func(res *chromeext.ActResult) error {
		if asJSON {
			printJSON(res)
			return nil
		}
		if res.Text != "" {
			fmt.Println(res.Text)
		}
		return nil
	}
	simple := func(use, zh, en string, args cobra.PositionalArgs, build func(args []string) (string, chromeext.ActArgs, error)) *cobra.Command {
		return &cobra.Command{
			Use: use, Short: i18n.S(zh, en), Args: args,
			RunE: func(_ *cobra.Command, args []string) error {
				action, a, err := build(args)
				if err != nil {
					return err
				}
				res, err := act(action, a)
				if err != nil {
					return err
				}
				return show(res)
			},
		}
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: i18n.S("开着的页面、连着的 Chrome、允许的网站", "Open pages, connected Chromes, allowed sites"),
		Args:  cobra.NoArgs,
		RunE:  func(_ *cobra.Command, _ []string) error { return browserStatus(asJSON) },
	})

	var why, profile, size string
	var focus, chrome, dock bool
	open := &cobra.Command{
		Use:   "open <url>",
		Short: i18n.S("开一个页面（默认在项目浏览器里，显示在浏览器面板），等它加载完，打印页面号", "Open a page (in the project's browser by default, shown in the Browser pane), wait for it to load, print its number"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			w, h := 0, 0
			if size != "" {
				var err error
				if w, h, err = parseSize(size); err != nil {
					return err
				}
			}
			t, err := browserOpen(args[0], why, profile, chrome, focus, dock, w, h)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(t)
				return nil
			}
			fmt.Printf("tab %d\n", t.Tab)
			if t.WS != "" {
				fmt.Printf("cdp %s\n", t.WS)
			}
			if t.Project != "" {
				if dock {
					fmt.Printf(i18n.S("在项目 %s 的浏览器里，用户在旁边的浏览器面板里看得到、能操作\n", "in project %s's browser, beside the chat in the Browser pane, where the user can act\n"), t.Project)
				} else {
					fmt.Printf(i18n.S("在项目 %s 的浏览器里，浮在角落给用户看；要用户来操作（登录、验证码）先 gerdr browser show\n", "in project %s's browser, floating in a corner for the user to watch; before asking the user to act (sign in, a captcha) run gerdr browser show\n"), t.Project)
				}
			}
			return nil
		},
	}
	open.Flags().BoolVar(&dock, "show", false, i18n.S("面板直接停在对话旁边（真页面，用户要操作时用）；默认浮在角落", "dock the pane beside the chat (the real page, for the user to act in); default: floating in a corner"))
	open.Flags().BoolVar(&chrome, "chrome", false, i18n.S("在用户自己的 Chrome 里开（经浏览器插件，用户已登录的账号）", "open in the user's own Chrome (through the browser extension, their signed-in accounts)"))
	open.Flags().StringVar(&why, "why", "", i18n.S("--chrome：问用户允许网站时窗口上写的理由", "--chrome: the reason shown when the user is asked to allow the site"))
	open.Flags().StringVar(&profile, "profile", "", i18n.S("--chrome：用哪个 Chrome profile，用户在插件弹窗里起的名字（如 Work）或 status 里的 id；默认按网站选", "--chrome: which Chrome profile, the name the user gave it in the extension's popup (like Work) or an id from status; default: picked by site"))
	open.Flags().BoolVar(&focus, "focus", false, i18n.S("--chrome：把窗口调到前台（默认在后面开）", "--chrome: bring the window to the front (default: opens behind)"))
	addProjectFlag(open)
	open.Flags().StringVar(&size, "size", "", i18n.S("窗口大小，如 1440x900", "window size, like 1440x900"))
	cmd.AddCommand(open)

	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: i18n.S("把页面的面板从角落停到对话旁边（真页面），要用户来操作（登录、验证码）前用", "Dock the page's pane beside the chat (the real page), before asking the user to act in it (sign in, a captcha)"),
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return browserShow(tab)
		},
	})

	var keep bool
	closeCmd := &cobra.Command{
		Use:   "close",
		Short: i18n.S("关掉窗口（--keep：留给用户，不再操作）", "Close the window (--keep: leave it to the user, no longer driven)"),
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c := artifactsClient()
			return c.do("POST", "/api/browser/close", map[string]any{"tab": tab, "keep": keep, "from": c.caller().Session}, nil)
		},
	}
	closeCmd.Flags().BoolVar(&keep, "keep", false, i18n.S("窗口留给用户", "leave the window to the user"))
	cmd.AddCommand(closeCmd)

	// --- reading the page ---
	var snap chromeext.ActArgs
	snapCmd := simple("snapshot", "页面大纲：可操作的东西和结构，每项一个 ref（e12），后面的操作用它", "An outline of the page: what can be acted on, and its structure, each with a ref (e12) for the other actions",
		cobra.NoArgs, func([]string) (string, chromeext.ActArgs, error) { return "snapshot", snap, nil })
	snapCmd.Flags().BoolVar(&snap.All, "all", false, i18n.S("也列出正文文字", "list the text too"))
	snapCmd.Flags().BoolVar(&snap.Viewport, "viewport", false, i18n.S("只列窗口里看得到的", "only what is in the window now"))
	snapCmd.Flags().StringVar(&snap.Find, "find", "", i18n.S("只列包含这段文字的行", "only lines containing this text"))
	snapCmd.Flags().IntVar(&snap.Max, "max", 0, i18n.S("最多几行（默认 400）", "at most this many lines (default 400)"))
	cmd.AddCommand(snapCmd)

	var textMax int
	textCmd := simple("text", "页面的地址、标题和正文文字", "The page's URL, title and visible text", cobra.NoArgs,
		func([]string) (string, chromeext.ActArgs, error) { return "text", chromeext.ActArgs{Max: textMax}, nil })
	textCmd.Flags().IntVar(&textMax, "max", 0, i18n.S("最多几个字（默认 50000）", "at most this many characters (default 50000)"))
	cmd.AddCommand(textCmd)

	var shot chromeext.ActArgs
	shotCmd := &cobra.Command{
		Use:   "screenshot [file]",
		Short: i18n.S("截图存到文件（默认临时目录），打印路径。图按 CSS 像素，图上的坐标就是 click x,y", "Save a screenshot (default: a temp file) and print its path. It is in CSS pixels: a point on it is click x,y"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			file := filepath.Join(os.TempDir(), fmt.Sprintf("gerdr-browser-%d.png", time.Now().UnixMilli()))
			if len(args) == 1 {
				file = args[0]
			}
			if l := strings.ToLower(file); strings.HasSuffix(l, ".jpg") || strings.HasSuffix(l, ".jpeg") {
				shot.Format = "jpeg"
			}
			res, err := act("screenshot", shot)
			if err != nil {
				return err
			}
			b, err := base64.StdEncoding.DecodeString(res.Image)
			if err != nil {
				return err
			}
			if err := os.WriteFile(file, b, 0o644); err != nil {
				return err
			}
			fmt.Println(file)
			return nil
		},
	}
	shotCmd.Flags().BoolVar(&shot.Full, "full", false, i18n.S("整页（不只是窗口里看得到的部分）", "the whole page, not just what the window shows"))
	shotCmd.Flags().StringVar(&shot.Target, "ref", "", i18n.S("只截这个元素", "only this element"))
	cmd.AddCommand(shotCmd)

	// --- acting ---
	var click chromeext.ActArgs
	var right bool
	clickCmd := simple("click <ref|x,y>", "点击（真实鼠标事件）。目标是 snapshot 的 ref，或截图上的坐标", "Click (real mouse events). The target is a ref from snapshot, or coordinates on a screenshot",
		cobra.ExactArgs(1), func(args []string) (string, chromeext.ActArgs, error) {
			a := click
			a.Target = args[0]
			if right {
				a.Button = "right"
			}
			return "click", a, nil
		})
	clickCmd.Flags().BoolVar(&click.Double, "double", false, i18n.S("双击", "double-click"))
	clickCmd.Flags().BoolVar(&right, "right", false, i18n.S("右键", "right-click"))
	clickCmd.Flags().StringSliceVar(&click.Modifiers, "mod", nil, i18n.S("按住的键：cmd、ctrl、shift、alt", "keys held: cmd, ctrl, shift, alt"))
	cmd.AddCommand(clickCmd)

	cmd.AddCommand(simple("hover <ref|x,y>", "鼠标移上去", "Move the mouse over it", cobra.ExactArgs(1),
		func(args []string) (string, chromeext.ActArgs, error) {
			return "hover", chromeext.ActArgs{Target: args[0]}, nil
		}))
	cmd.AddCommand(simple("drag <from> <to>", "按住拖到另一处（ref 或 x,y）", "Drag from one place to another (refs or x,y)", cobra.ExactArgs(2),
		func(args []string) (string, chromeext.ActArgs, error) {
			return "drag", chromeext.ActArgs{Target: args[0], To: args[1]}, nil
		}))

	var into string
	var submit bool
	typeCmd := simple("type <text|->", "输入文字（到 --into 的元素，或当前焦点）。- 从标准输入读", "Type text (into the --into element, or where the focus is). - reads stdin", cobra.ExactArgs(1),
		func(args []string) (string, chromeext.ActArgs, error) {
			text, err := argOrStdin(args[0])
			return "type", chromeext.ActArgs{Text: text, Target: into, Submit: submit}, err
		})
	typeCmd.Flags().StringVar(&into, "into", "", i18n.S("先聚焦这个元素（ref 或 x,y）", "focus this element first (ref or x,y)"))
	typeCmd.Flags().BoolVar(&submit, "submit", false, i18n.S("输完按回车", "press Enter afterwards"))
	cmd.AddCommand(typeCmd)

	var repeat int
	keyCmd := simple("key <key>...", "按键：Enter、Tab、Escape、Backspace、ArrowDown、cmd+a、shift+Tab…，几个依次按", "Press keys: Enter, Tab, Escape, Backspace, ArrowDown, cmd+a, shift+Tab…, several in turn",
		cobra.MinimumNArgs(1), func(args []string) (string, chromeext.ActArgs, error) {
			return "key", chromeext.ActArgs{Keys: args, Repeat: repeat}, nil
		})
	keyCmd.Flags().IntVar(&repeat, "repeat", 1, i18n.S("重复几遍", "how many times"))
	cmd.AddCommand(keyCmd)

	cmd.AddCommand(simple("fill <ref> <value>...", "填表单：输入框换成这个值（空串清空），下拉框选这项（几项是多选），勾选框 true / false", "Fill a form field: a text field gets this value (\"\" clears it), a select picks this option (several for multi-select), a checkbox true / false",
		cobra.MinimumNArgs(2), func(args []string) (string, chromeext.ActArgs, error) {
			v := args[1]
			a := chromeext.ActArgs{Target: args[0], Value: &v}
			if len(args) > 2 {
				a.Value, a.Values = nil, args[1:]
			}
			return "fill", a, nil
		}))

	var amount int
	var on string
	scrollCmd := simple("scroll <down|up|left|right|ref>", "滚动（鼠标滚轮）；给 ref 就把它滚到视野里", "Scroll (mouse wheel); a ref scrolls it into view", cobra.ExactArgs(1),
		func(args []string) (string, chromeext.ActArgs, error) {
			if refRe.MatchString(args[0]) {
				return "scroll", chromeext.ActArgs{Target: args[0], Direction: "into-view"}, nil
			}
			return "scroll", chromeext.ActArgs{Direction: args[0], Target: on, Amount: amount}, nil
		})
	scrollCmd.Flags().IntVar(&amount, "amount", 3, i18n.S("滚几格（一格 100 像素）", "wheel ticks (100 px each)"))
	scrollCmd.Flags().StringVar(&on, "on", "", i18n.S("在这个元素上滚（可滚动的面板）", "scroll over this element (a scrollable panel)"))
	cmd.AddCommand(scrollCmd)

	var wait chromeext.ActArgs
	var waitFor, waitTimeout time.Duration
	waitCmd := simple("wait", "等：--text 出现、--gone 消失、--selector 出现、--url 变成、--load 加载完，或 --for 一段时间", "Wait until --text appears, --gone disappears, --selector shows, the --url has, the page --load(s), or --for a while",
		cobra.NoArgs, func([]string) (string, chromeext.ActArgs, error) {
			a := wait
			a.Time = int(waitFor.Milliseconds())
			a.Timeout = int(waitTimeout.Milliseconds())
			return "wait", a, nil
		})
	waitCmd.Flags().StringVar(&wait.Text, "text", "", i18n.S("等这段文字出现", "until this text appears"))
	waitCmd.Flags().StringVar(&wait.Gone, "gone", "", i18n.S("等这段文字消失", "until this text is gone"))
	waitCmd.Flags().StringVar(&wait.Selector, "selector", "", i18n.S("等这个 CSS 选择器的元素出现", "until an element matching this CSS selector shows"))
	waitCmd.Flags().StringVar(&wait.URL, "url", "", i18n.S("等地址里有这段", "until the URL contains this"))
	waitCmd.Flags().BoolVar(&wait.Load, "load", false, i18n.S("等页面加载完", "until the page has loaded"))
	waitCmd.Flags().DurationVar(&waitFor, "for", 0, i18n.S("就等这么久，如 2s", "just wait this long, like 2s"))
	waitCmd.Flags().DurationVar(&waitTimeout, "timeout", 30*time.Second, i18n.S("最多等多久", "give up after"))
	cmd.AddCommand(waitCmd)

	cmd.AddCommand(simple("goto <url>", "跳到一个地址，等加载完", "Go to a URL and wait for it to load", cobra.ExactArgs(1),
		func(args []string) (string, chromeext.ActArgs, error) {
			return "goto", chromeext.ActArgs{URL: args[0]}, nil
		}))
	for _, n := range []struct{ name, zh, en string }{{"back", "后退", "Go back"}, {"forward", "前进", "Go forward"}, {"reload", "刷新", "Reload"}} {
		cmd.AddCommand(simple(n.name, n.zh, n.en, cobra.NoArgs, func([]string) (string, chromeext.ActArgs, error) { return n.name, chromeext.ActArgs{}, nil }))
	}

	cmd.AddCommand(simple("eval <js|->", "在页面里执行 JavaScript，打印结果（JSON；Promise 会等）。- 从标准输入读", "Run JavaScript in the page and print the result (JSON; promises are awaited). - reads stdin",
		cobra.ExactArgs(1), func(args []string) (string, chromeext.ActArgs, error) {
			expr, err := argOrStdin(args[0])
			return "eval", chromeext.ActArgs{Expr: expr}, err
		}))

	// --- what the page did ---
	var logs chromeext.ActArgs
	consoleCmd := simple("console", "页面的 console 输出和报错（窗口打开后一直在记）", "The page's console messages and errors (logged since the window opened)", cobra.NoArgs,
		func([]string) (string, chromeext.ActArgs, error) { return "console", logs, nil })
	consoleCmd.Flags().BoolVar(&logs.Errors, "errors", false, i18n.S("只看错误", "errors only"))
	consoleCmd.Flags().StringVar(&logs.Filter, "filter", "", i18n.S("只看包含这段的", "only those containing this"))
	consoleCmd.Flags().IntVar(&logs.Limit, "limit", 0, i18n.S("最多几条（默认最近 100）", "at most this many (default: the last 100)"))
	consoleCmd.Flags().BoolVar(&logs.Clear, "clear", false, i18n.S("看完清空", "clear after reading"))
	cmd.AddCommand(consoleCmd)

	var nets chromeext.ActArgs
	netCmd := simple("network", "页面发过的请求（id、方法、状态、类型、地址）；body <id> 看响应内容", "Requests the page made (id, method, status, type, URL); body <id> shows a response", cobra.NoArgs,
		func([]string) (string, chromeext.ActArgs, error) { return "network", nets, nil })
	netCmd.Flags().StringVar(&nets.Filter, "filter", "", i18n.S("地址里包含这段", "URL contains this"))
	netCmd.Flags().StringVar(&nets.Type, "type", "", i18n.S("资源类型：XHR、Fetch、Document、Script…", "resource type: XHR, Fetch, Document, Script…"))
	netCmd.Flags().IntVar(&nets.Limit, "limit", 0, i18n.S("最多几条（默认最近 100）", "at most this many (default: the last 100)"))
	netCmd.Flags().BoolVar(&nets.Clear, "clear", false, i18n.S("看完清空", "clear after reading"))
	cmd.AddCommand(netCmd)

	cmd.AddCommand(simple("body <id>", "一个请求的响应内容（network 里的 id）", "A request's response body (the id from network)", cobra.ExactArgs(1),
		func(args []string) (string, chromeext.ActArgs, error) {
			return "body", chromeext.ActArgs{ID: args[0]}, nil
		}))

	cmd.AddCommand(simple("upload <ref> <file>...", "把本机文件放进文件输入框（snapshot 里的 file）", "Put local files into a file input (file in snapshot)", cobra.MinimumNArgs(2),
		func(args []string) (string, chromeext.ActArgs, error) {
			files := make([]string, len(args)-1)
			for i, f := range args[1:] {
				abs, err := filepath.Abs(expandHome(f))
				if err != nil {
					return "", chromeext.ActArgs{}, err
				}
				files[i] = abs
			}
			return "upload", chromeext.ActArgs{Target: args[0], Files: files}, nil
		}))

	var mobile bool
	resizeCmd := simple("resize <WxH|reset>", "页面视口大小（模拟），reset 恢复成窗口大小", "The page's viewport size (emulated); reset goes back to the window's", cobra.ExactArgs(1),
		func(args []string) (string, chromeext.ActArgs, error) {
			if args[0] == "reset" {
				return "resize", chromeext.ActArgs{Reset: true}, nil
			}
			w, h, err := parseSize(args[0])
			return "resize", chromeext.ActArgs{Width: w, Height: h, Mobile: mobile}, err
		})
	resizeCmd.Flags().BoolVar(&mobile, "mobile", false, i18n.S("当成手机（触摸、移动版页面）", "as a phone (touch, mobile layout)"))
	cmd.AddCommand(resizeCmd)

	var promptText string
	dialogCmd := simple("dialog <accept|dismiss>", "回答页面弹出的 alert / confirm / prompt（开着时页面停着）", "Answer the page's alert / confirm / prompt (the page waits while one is open)", cobra.ExactArgs(1),
		func(args []string) (string, chromeext.ActArgs, error) {
			switch args[0] {
			case "accept", "ok", "yes":
				return "dialog", chromeext.ActArgs{Accept: true, Text: promptText}, nil
			case "dismiss", "cancel", "no":
				return "dialog", chromeext.ActArgs{}, nil
			}
			return "", chromeext.ActArgs{}, errors.New("accept or dismiss")
		})
	dialogCmd.Flags().StringVar(&promptText, "text", "", i18n.S("prompt 里填的字", "the text for a prompt"))
	cmd.AddCommand(dialogCmd)

	cmd.AddCommand(simple("cdp <Domain.method> [params-json]", "发一条 DevTools 命令，打印结果（插件只放行 Page、Runtime、DOM、Input 等域，cookie / 存储类不放行）", "Send one DevTools command and print its result (the extension allows Page, Runtime, DOM, Input and similar domains; not cookies / storage)",
		cobra.RangeArgs(1, 2), func(args []string) (string, chromeext.ActArgs, error) {
			a := chromeext.ActArgs{Method: args[0]}
			if len(args) == 2 {
				if !json.Valid([]byte(args[1])) {
					return "", a, errors.New(i18n.S("参数不是合法的 JSON", "params aren't valid JSON"))
				}
				a.Params = json.RawMessage(args[1])
			}
			return "cdp", a, nil
		}))

	cmd.AddCommand(browserMCPCmd())
	return cmd
}

var refRe = regexp.MustCompile(`^e\d+$`)

func browserAct(tab int, action string, a chromeext.ActArgs) (*chromeext.ActResult, error) {
	c, err := newAPIClient()
	if err != nil {
		return nil, err
	}
	c.http.Timeout = 11 * time.Minute // wait can be long
	var res chromeext.ActResult
	if err := c.do("POST", "/api/browser/act", map[string]any{"tab": tab, "action": action, "args": a}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

type browserOpened struct {
	Tab     int    `json:"tab"`
	Profile string `json:"profile,omitempty"`
	Project string `json:"project,omitempty"`
	URL     string `json:"url"`
	WS      string `json:"ws,omitempty"`
}

// browserOpen: chrome / profile → the user's Chrome; else the caller's project's browser.
// show: the pane docked beside the agent as the real page, not the floating corner it gets by default (the user is to
// act in it: sign in, a captcha)
func browserOpen(url, why, profile string, chrome, focus, show bool, w, h int) (*browserOpened, error) {
	c, err := newAPIClient()
	if err != nil {
		return nil, err
	}
	c.http.Timeout = 11 * time.Minute // the user may have to allow the site first
	cl := c.caller()
	body := map[string]any{"url": url, "why": why, "profile": profile, "chrome": chrome, "focused": focus,
		"project": cl.Project, "from": cl.Session, "term": cl.Term, "show": show}
	if w > 0 && h > 0 {
		body["width"], body["height"] = w, h
	}
	var t browserOpened
	if err := c.do("POST", "/api/browser/open", body, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// browserShow: the page's pane docked beside the calling agent (POST /api/browser/show).
func browserShow(tab int) error {
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	cl := c.caller()
	return c.do("POST", "/api/browser/show", map[string]any{"tab": tab, "project": cl.Project, "from": cl.Session, "term": cl.Term}, nil)
}

func argOrStdin(s string) (string, error) {
	if s != "-" {
		return s, nil
	}
	b, err := io.ReadAll(os.Stdin)
	return string(b), err
}

func parseSize(s string) (int, int, error) {
	ws, hs, ok := strings.Cut(strings.ToLower(s), "x")
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if !ok || err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("%s: %q", i18n.S("大小写成 宽x高，如 1280x800", "write the size as WIDTHxHEIGHT, like 1280x800"), s)
	}
	return w, h, nil
}

type browserState struct {
	Profiles []struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Version string   `json:"version"`
		Sites   []string `json:"sites"`
	} `json:"profiles"`
	Tabs []struct {
		Tab     int    `json:"tab"`
		Project string `json:"project,omitempty"`
		URL     string `json:"url"`
		WS      string `json:"ws,omitempty"`
		Gone    string `json:"gone,omitempty"`
	} `json:"tabs"`
}

func getBrowserState() (*browserState, error) {
	c, err := newAPIClient()
	if err != nil {
		return nil, err
	}
	var st browserState
	if err := c.do("GET", "/api/browser", nil, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (st *browserState) text() string {
	var b strings.Builder
	if len(st.Profiles) == 0 {
		b.WriteString(i18n.S("用户自己的 Chrome 没连上（--chrome 要它：Chrome 开着、装了 Gerdr 浏览器插件；安装入口在 Gerdr 的 设置 → 浏览器）\n", "The user's own Chrome isn't connected (--chrome needs it: Chrome open with the Gerdr browser extension; install it from Gerdr's Settings → Browser)\n"))
	}
	for _, p := range st.Profiles {
		if p.Name != "" {
			fmt.Fprintf(&b, "chrome %q %s (extension %s)\n", p.Name, p.ID, p.Version)
		} else {
			fmt.Fprintf(&b, "chrome %s (extension %s)\n", p.ID, p.Version)
		}
		for _, s := range p.Sites {
			fmt.Fprintf(&b, "  allowed %s\n", s)
		}
	}
	for _, t := range st.Tabs {
		where := t.WS
		if t.Project != "" {
			where = i18n.S("项目 ", "project ") + t.Project
		}
		line := fmt.Sprintf("tab %d  %s  %s", t.Tab, t.URL, where)
		if t.Gone != "" {
			line += "  (" + t.Gone + ")"
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func browserStatus(asJSON bool) error {
	st, err := getBrowserState()
	if err != nil {
		return err
	}
	if asJSON {
		printJSON(st)
		return nil
	}
	fmt.Print(st.text())
	return nil
}
