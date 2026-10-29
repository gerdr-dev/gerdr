package native

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 长期记忆：跨会话记住「代码里看不出来」的东西 —— 用户的偏好、用户纠正过的做法、项目的背景和约定。
// 照 Claude Code 的自动记忆做，基于文件、不用向量库：
//
//   - 两层目录：全局（<dir>/memory/global，人的偏好）和项目（<dir>/memory/projects/<git 根的路径>，项目的事）；
//   - 一条记忆一个 .md（开头 frontmatter：name / description / type），每层一个 MEMORY.md 索引，一条一行；
//   - 两层的 MEMORY.md 原样放进系统提示词（只是索引，占不了多少），哪条相关模型自己 read 全文；
//   - 不加新工具：模型用 write / edit 写、bash rm 删，提示词里教它什么该记、什么不该记。往记忆目录里写 .md 不审批
//     （permission.go 的 memoryWrite）—— 每记一条都要人点一下，就没人会让它记了。
//
// 为什么不每轮自动抽记忆（ChatGPT 那样再调一次小模型）：每轮多花钱，还容易记进一堆没用的；该记的时候让它自己记。
// 和 Claude Code 的记忆分开存（用户定的）：两边各记各的，不会互相写乱。

const memoryIndex = "MEMORY.md"

// 索引最多放进提示词多少：太长的索引本身就是问题（该合并、该删了），截断时告诉模型
const memoryIndexMax = 25_000

type memoryDirs struct {
	Global  string
	Project string // cwd 不在任何目录下（没有 git 也没有 cwd）时为空
}

func (m memoryDirs) all() []string {
	var out []string
	for _, d := range []string{m.Global, m.Project} {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

var slugChars = regexp.MustCompile(`[^A-Za-z0-9]+`)

// memoryFor：这个工作目录的两层记忆目录。项目按 git 根认（同一个仓库的子目录共用一份），没有 git 就按 cwd。
// 目录名是路径的可读变形（/Users/me/dev/app → Users-me-dev-app），太长才截断加哈希：人去翻目录时认得出是哪个项目。
func memoryFor(root, cwd string) memoryDirs {
	base := filepath.Join(root, "memory")
	m := memoryDirs{Global: filepath.Join(base, "global")}
	key := gitRoot(cwd)
	if key == "" {
		key = cwd
	}
	if key == "" {
		return m
	}
	slug := strings.Trim(slugChars.ReplaceAllString(key, "-"), "-")
	if slug == "" {
		slug = "root"
	}
	if len(slug) > 100 {
		h := sha256.Sum256([]byte(key))
		slug = slug[len(slug)-90:] + "-" + hex.EncodeToString(h[:4])
	}
	m.Project = filepath.Join(base, "projects", slug)
	return m
}

// inMemory：p 是不是某个记忆目录里的 .md 文件。
func (m memoryDirs) inMemory(p string) bool {
	if !strings.EqualFold(filepath.Ext(p), ".md") {
		return false
	}
	for _, d := range m.all() {
		if within(p, d) && filepath.Clean(p) != filepath.Clean(d) {
			return true
		}
	}
	return false
}

const memoryPrompt = `# Memory

You have a persistent, file-based memory that survives across conversations. Use it so the user never has to tell you the same thing twice.

There are two memory directories:
- Global (about the user, applies everywhere): %s
- Project (about this project only): %s

Each memory is one Markdown file holding one fact, with frontmatter:

` + "```markdown" + `
---
name: short-kebab-case-slug
description: one-line summary, used to decide relevance later
type: user | feedback | project | reference
---

The fact. For feedback and project memories, follow it with a **Why:** line and a **How to apply:** line.
` + "```" + `

Types: user = who the user is and their preferences; feedback = guidance the user gave on how you should work (corrections, and approaches they confirmed), with the reason; project = ongoing work, goals, decisions and constraints that cannot be derived from the code or git history (convert relative dates to absolute dates); reference = pointers to external resources (URLs, dashboards, tickets).

After writing a memory file, add a one-line pointer to the MEMORY.md index in the same directory: ` + "`- [Title](file.md) — hook`" + `. MEMORY.md is only an index — one line per memory, never the content itself. Create the directory and MEMORY.md if they do not exist yet; writing Markdown files in the memory directories does not need the user's approval.

When to save: the user corrects you or tells you how they want things done; they confirm a non-obvious approach; you learn a durable fact about them or the project that you would otherwise have to ask again. Save it right away, without asking. Do not save what the repository already records (code structure, past fixes, git history, AGENTS.md / CLAUDE.md content), secrets, or things that only matter to the current conversation.
Before saving, check the index for an existing memory on the same subject and update that file instead of creating a duplicate. When a memory turns out to be wrong or outdated, fix or delete it (` + "`rm`" + ` the file and remove its index line).
When the user asks what you remember, read the indexes and the relevant files. When the user asks you to forget something, delete it.
Memories were true when written: if one names a file, function or flag, check it still exists before relying on it.
`

// memorySection：提示词里关于记忆的一段 —— 怎么用，加上两层 MEMORY.md 的内容。
func memorySection(m memoryDirs) string {
	var b strings.Builder
	project := m.Project
	if project == "" {
		project = "(none: no working directory)"
	}
	fmt.Fprintf(&b, memoryPrompt, m.Global, project)
	for _, x := range []struct{ label, dir string }{{"global", m.Global}, {"project", m.Project}} {
		if x.dir == "" {
			continue
		}
		p := filepath.Join(x.dir, memoryIndex)
		raw, err := os.ReadFile(p)
		content := strings.TrimSpace(string(raw))
		if err != nil || content == "" {
			fmt.Fprintf(&b, "\n## Your %s memory index (%s)\n(empty — nothing saved yet)\n", x.label, p)
			continue
		}
		if len(content) > memoryIndexMax {
			content = truncUTF8(content, memoryIndexMax) + "\n[index truncated: it is too long — merge or delete entries]"
		}
		fmt.Fprintf(&b, "\n## Your %s memory index (%s)\n%s\n", x.label, p, content)
	}
	return b.String()
}

// memoryTitle：写记忆的工具调用在界面上的标题（不然是一长串 ../../.gerdr/native/memory/… 的路径）。
func memoryTitle(m memoryDirs, p string) string {
	name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	scope := "项目"
	if m.Global != "" && within(p, m.Global) {
		scope = "全局"
	}
	if filepath.Base(p) == memoryIndex {
		return "更新" + scope + "记忆索引"
	}
	return "记住（" + scope + "）：" + name
}

// ProjectMemorySlug：dir 这个项目的记忆目录名（设置页把目录名认回项目路径用，internal/memstore）。
func ProjectMemorySlug(dir string) string {
	p := memoryFor("/", dir).Project
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}
