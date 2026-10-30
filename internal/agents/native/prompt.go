package native

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/ext"
)

// 系统提示词：每轮现拼，但内容只随文件变（日期按天），前缀缓存照样命中。
//
// 指令文件：用户已经给 Claude Code / Codex 写过的那些（~/.claude/CLAUDE.md、项目里的 AGENTS.md / CLAUDE.md）
// 原样带上 —— 用户的规矩（用中文回复、改完跑什么测试）不用再给这个 agent 写一遍。

const basePrompt = `You are a coding agent running inside gerdr, working on the user's computer through tools. You help with software engineering and other tasks in the working directory.

# How to work
- Be concise and direct. Do the work instead of describing it; when you have enough information to act, act.
- Keep the user posted while you work: before your first tool call, say in a sentence what you're about to do; while working, give a brief update when you find something that matters or change direction. Write these in the user's language, for someone glancing over — not a log of every step.
- Before changing code, read the relevant files and follow the conventions you find (naming, comment density, libraries already in use). Do not add new dependencies without need.
- Use the dedicated tools: read (not cat), edit/write (not sed or heredocs), grep/glob (not grep/find in bash). Use bash for building, testing, git and everything else.
- Independent tool calls can be made in the same response; they run in parallel when they are read-only.
- For multi-step tasks keep a todo list with todo_write and update it as you go.
- Verify your work: run the tests or the build when there are any. Report results faithfully: if something failed or was skipped, say so.
- Use the task tool to hand a self-contained, research-heavy sub-task to a sub-agent (e.g. searching a large codebase); it returns only a summary, which keeps your context small.
- Ask the user (ask_user) only when a decision is genuinely theirs; otherwise pick the sensible default and mention it.
- Do not commit, push, or take destructive or outward-facing actions (deleting data, sending messages, deploying) unless the user asked for it.
- Refer to code as path:line so the user can click it.
- When the conversation runs long, earlier parts may be replaced by a summary; continue from it.`

const subagentPrompt = `You are a sub-agent of a coding agent running inside gerdr. You were given one self-contained task. Work on it with the tools, then reply with a concise report of what you found or did — that final message is the only thing the parent agent sees, so include the concrete facts (paths, line numbers, commands, results) it needs. Do not ask the user questions.`

// systemPrompt：mem 为 nil = 子 agent（任务是主 agent 给的，不用指令文件、skill、记忆）。
func systemPrompt(cwd string, skills []ext.Skill, model resolved, mem *memoryDirs) string {
	sub := mem == nil
	var b strings.Builder
	if sub {
		b.WriteString(subagentPrompt)
	} else {
		b.WriteString(basePrompt)
	}
	b.WriteString("\n\n# Environment\n")
	fmt.Fprintf(&b, "- Working directory: %s\n", cwd)
	if root := gitRoot(cwd); root != "" {
		fmt.Fprintf(&b, "- Git repository root: %s\n", root)
	} else {
		b.WriteString("- Not a git repository\n")
	}
	fmt.Fprintf(&b, "- Platform: %s/%s, shell: %s\n", runtime.GOOS, runtime.GOARCH, shell())
	fmt.Fprintf(&b, "- Today's date: %s\n", time.Now().Format("2006-01-02"))
	// 不写的话问「你是什么模型」它只能猜（用户问过）
	if model.ID != "" {
		pn := model.Provider.Name
		if pn == "" {
			pn = model.Provider.ID
		}
		fmt.Fprintf(&b, "- You are the model %q (shown to the user as %q), called through the %q API provider\n", model.ID, model.Name, pn)
	}

	if !sub {
		for _, f := range instructionFiles(cwd) {
			fmt.Fprintf(&b, "\n# Instructions from %s\n\n%s\n", f.path, strings.TrimSpace(f.content))
		}
		if len(skills) > 0 {
			b.WriteString("\n# Skills\nSkills are packaged instructions for specific tasks. When a task matches a skill's description, read its SKILL.md with the read tool first and follow it (paths inside are relative to the skill's directory). The user may also invoke one as /<name>.\n")
			for _, s := range skills {
				fmt.Fprintf(&b, "- %s: %s (%s)\n", s.Name, oneLine(s.Description, 300), filepath.Join(s.Dir, "SKILL.md"))
			}
		}
		b.WriteString("\n" + memorySection(*mem))
		b.WriteString("\n" + acpx.GUIHint + "\n")
	}
	return b.String()
}

type instrFile struct{ path, content string }

// instructionFiles：用户级的（~/.claude/CLAUDE.md、~/.codex/AGENTS.md）+ 从 git 根（没有就 cwd）一路到 cwd 每层的
// AGENTS.md / CLAUDE.md。同一层两个文件内容一样（一个是另一个的链接）只带一份。
func instructionFiles(cwd string) []instrFile {
	home, _ := os.UserHomeDir()
	var out []instrFile
	seen := map[string]bool{}
	add := func(p string) {
		b, err := os.ReadFile(p)
		if err != nil || len(strings.TrimSpace(string(b))) == 0 {
			return
		}
		c := string(b)
		if len(c) > 40_000 {
			c = c[:40_000] + "\n[truncated]"
		}
		if seen[c] {
			return
		}
		seen[c] = true
		out = append(out, instrFile{p, c})
	}
	if home != "" {
		add(filepath.Join(home, ".claude", "CLAUDE.md"))
		add(filepath.Join(home, ".codex", "AGENTS.md"))
	}
	top := gitRoot(cwd)
	if top == "" {
		top = cwd
	}
	var dirs []string
	for d := cwd; ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if d == top || filepath.Dir(d) == d || d == home {
			break
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		add(filepath.Join(dirs[i], "AGENTS.md"))
		add(filepath.Join(dirs[i], "CLAUDE.md"))
	}
	return out
}

func gitRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// skillsFor：给这个 agent 的 skill —— 内置的（gerdr、gerdr-ui）总是给；「agent 自己的」当成 Claude Code 的那份
// （用户级、项目级：这个 agent 没有自己的 skill 目录，用户的 skill 基本都在 ~/.claude/skills 里）；
// 设置里的来源（只用 agent 的 / 两者 / 只用 gerdr 的）照常生效。同名的先到先得。
func skillsFor(m *ext.Manager, cwd string) []ext.Skill {
	var all []ext.Skill
	src := ext.SourceAgent
	if m != nil {
		all = append(all, m.Builtin()...)
		src = m.Settings().SkillSource
		if src.UsesGerdr() {
			all = append(all, m.Skills()...)
		}
	}
	if src != ext.SourceGerdr {
		all = append(all, ext.ClaudeSkills(cwd)...)
	}
	seen := map[string]bool{}
	var out []ext.Skill
	for _, s := range all {
		if !s.Enabled || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	return m.FilterSkills(out) // 角色限定了 skill 的话只留挑中的（m 是 nil 也行）
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
