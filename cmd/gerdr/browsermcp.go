package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/version"
)

// gerdr browser mcp: the `gerdr browser` actions as MCP tools on stdio, for agents that would rather call tools than
// run commands (screenshots come back as images). Add it with `gerdr mcp add browser -- gerdr browser mcp`.
func browserMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: i18n.S("把这些操作作为 MCP 工具提供（stdio）。加给 agent：gerdr mcp add browser -- gerdr browser mcp", "Serve these actions as MCP tools (stdio). Give them to agents: gerdr mcp add browser -- gerdr browser mcp"),
		Args:  cobra.NoArgs,
		RunE:  func(_ *cobra.Command, _ []string) error { return serveBrowserMCP() },
	}
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	action      string
}

func browserTools() []mcpTool {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	num := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	boolean := func(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
	strs := func(d string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": d}
	}
	target := str("A ref from snapshot (like e12), or viewport coordinates \"x,y\" in CSS pixels as on a screenshot")
	tool := func(name, action, desc string, props map[string]any, required ...string) mcpTool {
		if props == nil {
			props = map[string]any{}
		}
		if name != "status" && name != "open" {
			props["tab"] = num("Which window (from open); default: the one opened last")
		}
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return mcpTool{Name: name, Description: desc, InputSchema: s, action: action}
	}
	return []mcpTool{
		tool("status", "", "The pages open now, the Chrome profiles connected through the Gerdr extension and the sites the user allowed there.", nil),
		tool("open", "", "Open a page on a URL and wait for it to load; returns its number (tab). By default in the project's own browser: a profile per project that stays signed in, shown to the user in the Browser pane beside the chat, where they can sign in for you. With chrome (or profile): in the user's own Chrome through the Gerdr extension, signed in as they are; the first time a site is used there the extension asks the user to Allow it and this waits (up to 10 minutes): tell the user to look at Chrome.",
			map[string]any{"url": str("http(s) URL"), "chrome": boolean("Open in the user's own Chrome instead of the project's browser"), "size": str("Window size like 1440x900"),
				"why": str("chrome: shown to the user when asking to allow the site"), "focus": boolean("chrome: bring the window to the front"),
				"profile": str("chrome: which Chrome profile, the name the user gave it (status lists them, like Work) or its id; default: one that allowed the site")}, "url"),
		tool("close", "", "Close the window (keep: leave it open for the user, no longer driven).", map[string]any{"keep": boolean("Leave it to the user")}),
		tool("snapshot", "snapshot", "An outline of the page: everything that can be acted on (links, buttons, fields…) and the page's structure, each with a ref (e12) to use in click / type / fill / scroll / hover. Take a new one after the page changes.",
			map[string]any{"all": boolean("Include the text too"), "viewport": boolean("Only what is in the window now"), "find": str("Only lines containing this text"), "max": num("At most this many lines (default 400)")}),
		tool("text", "text", "The page's URL, title and visible text.", map[string]any{"max": num("At most this many characters (default 50000)")}),
		tool("screenshot", "screenshot", "A screenshot of the window (in CSS pixels: a point on it is a click x,y), the whole page, or one element.",
			map[string]any{"full": boolean("The whole page"), "target": str("Only this element (a ref)")}),
		tool("click", "click", "Click with real mouse events.", map[string]any{"target": target, "double": boolean("Double-click"), "button": str("left (default), right or middle"), "modifiers": strs("Keys held: cmd, ctrl, shift, alt")}, "target"),
		tool("hover", "hover", "Move the mouse over something.", map[string]any{"target": target}, "target"),
		tool("drag", "drag", "Drag from one place to another.", map[string]any{"target": target, "to": target}, "target", "to"),
		tool("type", "type", "Type text with real input (works in rich text editors). Into target if given (focused first), else where the focus is. Doesn't clear what is there: use fill for that.",
			map[string]any{"text": str("What to type"), "target": target, "submit": boolean("Press Enter afterwards")}, "text"),
		tool("key", "key", "Press keys, in turn: Enter, Tab, Escape, Backspace, Delete, Space, ArrowUp/Down/Left/Right, Home, End, PageUp, PageDown, F1-F12, one character; with modifiers joined by +: cmd+a, shift+Tab, ctrl+shift+ArrowLeft.",
			map[string]any{"keys": strs("Keys to press"), "repeat": num("How many times")}, "keys"),
		tool("fill", "fill", "Set a form field: a text field gets value (replacing what is there; \"\" clears), a select picks the option with that text or value (values for multi-select), a checkbox / radio / switch \"true\" or \"false\".",
			map[string]any{"target": str("A ref from snapshot"), "value": str("The value"), "values": strs("Several options for a multi-select")}, "target"),
		tool("scroll", "scroll", "Scroll with the mouse wheel, over the page or over target (a scrollable panel); direction into-view scrolls target into view.",
			map[string]any{"direction": str("up, down (default), left, right, into-view"), "target": target, "amount": num("Wheel ticks of 100 px (default 3)")}),
		tool("wait", "wait", "Wait until text appears, text is gone, a CSS selector shows, the URL contains something, the page loaded, or just for time ms.",
			map[string]any{"text": str("Until this text appears"), "gone": str("Until this text is gone"), "selector": str("Until this CSS selector shows"), "url": str("Until the URL contains this"),
				"load": boolean("Until the page has loaded"), "time": num("Just wait this many ms"), "timeout": num("Give up after this many ms (default 30000)")}),
		tool("navigate", "goto", "Go to a URL in the window and wait for it to load.", map[string]any{"url": str("http(s) URL")}, "url"),
		tool("back", "back", "Go back in history.", nil),
		tool("forward", "forward", "Go forward in history.", nil),
		tool("reload", "reload", "Reload the page.", nil),
		tool("eval", "eval", "Run JavaScript in the page; returns the value as JSON (promises are awaited).", map[string]any{"expr": str("A JavaScript expression")}, "expr"),
		tool("console", "console", "The page's console messages and uncaught errors, logged since the window opened.",
			map[string]any{"errors": boolean("Errors only"), "filter": str("Only those containing this"), "limit": num("The last this many (default 100)"), "clear": boolean("Clear after reading")}),
		tool("network", "network", "Requests the page made since the window opened: id, method, status, resource type, URL. Read one's response with response_body.",
			map[string]any{"filter": str("URL contains this"), "type": str("XHR, Fetch, Document, Script…"), "limit": num("The last this many (default 100)"), "clear": boolean("Clear after reading")}),
		tool("response_body", "body", "The response body of a request (the id from network): often the cleanest way to get a page's data.", map[string]any{"id": str("Request id"), "max": num("At most this many bytes (default 100000)")}, "id"),
		tool("upload", "upload", "Put local files into a file input (a ref shown as file in snapshot).", map[string]any{"target": str("The file input's ref"), "files": strs("Absolute paths")}, "target", "files"),
		tool("resize", "resize", "Emulate a viewport size (mobile: as a phone), or reset to the window's.",
			map[string]any{"width": num("CSS px"), "height": num("CSS px"), "mobile": boolean("As a phone"), "reset": boolean("Back to the window's size")}),
		tool("dialog", "dialog", "Answer the alert / confirm / prompt the page opened (the page is paused until then).", map[string]any{"accept": boolean("OK (true) or Cancel (false)"), "text": str("Text for a prompt")}, "accept"),
		tool("cdp", "cdp", "Send one raw DevTools command to the page (Page, Runtime, DOM, Input, Emulation, CSS, Network, Accessibility… ; cookies and storage are refused by the extension).",
			map[string]any{"method": str("Like DOM.getDocument"), "params": map[string]any{"type": "object", "description": "The command's parameters"}}, "method"),
	}
}

func serveBrowserMCP() error {
	tools := browserTools()
	byName := map[string]mcpTool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 32<<20)
	out := json.NewEncoder(os.Stdout)
	reply := func(id json.RawMessage, result any) {
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	for in.Scan() {
		var m rpcMsg
		if json.Unmarshal(in.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue // notifications
		}
		switch m.Method {
		case "initialize":
			reply(m.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "gerdr-browser", "version": version.Version},
				"instructions": "Work on web pages: in the project's own browser (shown in Gerdr's Browser pane, signed in per project) or, with open's chrome, in the user's own Chrome through the Gerdr extension. Loop: open → snapshot (refs like e12) → click / type / fill / key / scroll by ref → snapshot or screenshot to check → close. " +
					"Without tab, actions use the page opened last. A sign-in: ask the user to do it in the Browser pane. In the user's Chrome each site needs their Allow the first time (open waits for it).",
			})
		case "ping":
			reply(m.ID, map[string]any{})
		case "tools/list":
			reply(m.ID, map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			json.Unmarshal(m.Params, &p)
			reply(m.ID, callBrowserTool(byName, p.Name, p.Arguments))
		default:
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "method not found: " + m.Method}})
		}
	}
	return in.Err()
}

func callBrowserTool(byName map[string]mcpTool, name string, raw json.RawMessage) map[string]any {
	fail := func(err error) map[string]any {
		return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
	}
	text := func(s string) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}}
	}
	t, ok := byName[name]
	if !ok {
		return fail(fmt.Errorf("unknown tool %q", name))
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	var common struct {
		Tab     int    `json:"tab"`
		URL     string `json:"url"`
		Why     string `json:"why"`
		Size    string `json:"size"`
		Focus   bool   `json:"focus"`
		Keep    bool   `json:"keep"`
		Profile string `json:"profile"`
		Chrome  bool   `json:"chrome"`
	}
	if err := json.Unmarshal(raw, &common); err != nil {
		return fail(err)
	}
	switch name {
	case "status":
		st, err := getBrowserState()
		if err != nil {
			return fail(err)
		}
		return text(st.text())
	case "open":
		w, h := 0, 0
		if common.Size != "" {
			var err error
			if w, h, err = parseSize(common.Size); err != nil {
				return fail(err)
			}
		}
		o, err := browserOpen(common.URL, common.Why, common.Profile, common.Chrome, common.Focus, w, h)
		if err != nil {
			return fail(err)
		}
		return text(fmt.Sprintf("opened tab %d on %s", o.Tab, o.URL))
	case "close":
		c, err := newAPIClient()
		if err != nil {
			return fail(err)
		}
		if err := c.do("POST", "/api/browser/close", map[string]any{"tab": common.Tab, "keep": common.Keep, "from": c.caller().Session}, nil); err != nil {
			return fail(err)
		}
		return text("closed")
	}
	var a chromeext.ActArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return fail(err)
	}
	res, err := browserAct(common.Tab, t.action, a)
	if err != nil {
		return fail(err)
	}
	content := []any{}
	if res.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": res.Text})
	}
	if res.Image != "" {
		content = append(content, map[string]any{"type": "image", "data": res.Image, "mimeType": res.MIME})
	}
	return map[string]any{"content": content}
}
