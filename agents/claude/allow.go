package claude

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// auto 模式下分类器拦下的调用不走 can_use_tool（CLI 自己就拒了），只在工具结果里留一句：
//
//	Permission for this action was denied by the Claude Code auto mode classifier. Reason: …
//
// 界面上没有 claude 里 /permissions 那种「批准刚才被拦的」入口，所以适配器认出这句话，
// 给工具调用挂上能放行它的规则（_meta.gerdr.autoDenied），界面画「允许并重试」：
// 把规则写进 <cwd>/.claude/settings.local.json（和 claude 审批里「以后不再问」写的是同一个地方），
// 再发一句话让它重试。CLI 会监视设置文件，改了不用重启进程就生效（实测）。
//
// 试过另一条路：initialize 时注册 PreToolUse hook，放行这一次。但没能稳定造出分类器拒绝来验证
// hook 的 allow 压不压得过分类器，而 allow 规则压得过是确定的（规则先于分类器判）。
const classifierDenied = "denied by the Claude Code auto mode classifier"

// allowRules：能放行这次调用的权限规则，尽量窄（Bash 是这几条精确命令，不是前缀）。
// 返回 nil 表示不知道怎么写规则（界面就不给按钮）。
func allowRules(tool string, in map[string]any) []string {
	switch {
	case tool == "Bash":
		cmd := strings.TrimSpace(s(in, "command"))
		if cmd == "" {
			return nil
		}
		var out []string
		for _, c := range splitShell(cmd) {
			out = append(out, "Bash("+c+")")
		}
		return out
	case isEditTool(tool):
		p := s(in, "file_path")
		if p == "" {
			p = s(in, "notebook_path")
		}
		if !filepath.IsAbs(p) {
			return nil
		}
		// 规则里 `//` 开头是文件系统绝对路径（单个 `/` 是相对设置文件所在目录）。Edit 规则也管 Write。
		return []string{"Edit(/" + p + ")"}
	case tool == "WebFetch":
		u, err := url.Parse(s(in, "url"))
		if err != nil || u.Hostname() == "" {
			return nil
		}
		return []string{"WebFetch(domain:" + u.Hostname() + ")"}
	case strings.HasPrefix(tool, "mcp__"):
		return []string{tool}
	}
	return nil
}

// splitShell 把复合命令按顶层的 && || ; | 换行拆开。CLI 匹配 Bash 规则时就是逐段比的，
// 整条 `a && b` 写成一条精确规则匹配不上（实测）；它自己的审批建议也是一段一条。
// 引号里的不拆；带 $( ` 子命令、heredoc 的拆不准，整条返回（规则可能不生效，但不会放行别的）。
func splitShell(cmd string) []string {
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") || strings.Contains(cmd, "<<") {
		return []string{cmd}
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
		cur.Reset()
	}
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' && i+1 < len(cmd) {
				cur.WriteByte(c)
				i++
				c = cmd[i]
			} else if c == quote {
				quote = 0
			}
		case c == '\\' && i+1 < len(cmd):
			cur.WriteByte(c)
			i++
			c = cmd[i]
		case c == '\'' || c == '"':
			quote = c
		case c == ';' || c == '\n':
			flush()
			continue
		case c == '&' && i+1 < len(cmd) && cmd[i+1] == '&', c == '|' && i+1 < len(cmd) && cmd[i+1] == '|':
			flush()
			i++
			continue
		case c == '|':
			flush()
			continue
		}
		cur.WriteByte(c)
	}
	if quote != 0 {
		return []string{cmd}
	}
	flush()
	return out
}

// addAllowRules 把规则并进 <cwd>/.claude/settings.local.json 的 permissions.allow，别的键原样留着。
func addAllowRules(cwd string, rules []string) error {
	if cwd == "" || len(rules) == 0 {
		return errors.New("no cwd or rules")
	}
	path := filepath.Join(cwd, ".claude", "settings.local.json")
	doc := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return errors.New(path + " 不是合法的 JSON，没敢改：" + err.Error())
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	perms, _ := doc["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	for _, r := range rules {
		if !slices.Contains(allow, any(r)) {
			allow = append(allow, r)
		}
	}
	perms["allow"] = allow
	doc["permissions"] = perms
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".gerdr-tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
