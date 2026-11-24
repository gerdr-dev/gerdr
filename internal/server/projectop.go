package server

// `gerdr project list / new`: projects from the command line, so an agent asked to set someone up ("make an lb project,
// install the Superagent in it") can do it without sending them to the sidebar. A project is an entry in the layout's
// spaces plus the workspaces that carry its id; this does what ui/src/lib/actions/spaces.ts addSpace does, with the
// same name rules (ui/src/lib/layout.ts projectNameError) — keep the two in step.

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// reservedProjectNames are path segments of Gerdr's own (a project's id appears in addresses): layout.ts RESERVED.
var reservedProjectNames = map[string]bool{"work": true, "api": true, "auth": true, "rescue": true, "ws": true, "mcp-oauth": true, "_ui": true, "_esm": true, "projects": true}

var badProjectChars = regexp.MustCompile(`[/\\.\x00-\x1f]`)

// spaceList is layout.ts spaceList: the saved list, with default first if it's missing and any project a workspace
// names but the list doesn't.
func (c *layoutCall) spaceList() []node {
	out := nodes(c.l["spaces"])
	has := func(id string) bool {
		for _, s := range out {
			if str(s, "id") == id {
				return true
			}
		}
		return false
	}
	if !has(DefaultSpace) {
		out = append([]node{{"id": DefaultSpace, "name": "default"}}, out...)
	}
	for _, w := range c.wss() {
		if sp := spaceOfWs(w); !has(sp) {
			out = append(out, node{"id": sp, "name": sp})
		}
	}
	return out
}

func spaceName(s node) string {
	if n := str(s, "name"); n != "" {
		return n
	}
	return str(s, "id")
}

// projectNameError is layout.ts projectNameError: why name can't be a new project's ("" = it can).
func (c *layoutCall) projectNameError(name string) error {
	if name == "" {
		return errors.New(i18n.S("起个名字", "give it a name"))
	}
	if utf8.RuneCountInString(name) > 64 {
		return errors.New(i18n.S("名字太长了（最多 64 个字）", "name is too long (64 characters max)"))
	}
	if badProjectChars.MatchString(name) {
		return errors.New(i18n.S("名字里不能有 / \\ 和 .（名字会出现在地址和目录名里）", "names can't contain / \\ or . (they appear in addresses and directory names)"))
	}
	if reservedProjectNames[strings.ToLower(name)] {
		return errors.New(i18n.S("「"+name+"」是 Gerdr 自己用的名字，换一个", "\""+name+"\" is a name Gerdr uses itself; pick another"))
	}
	for _, s := range c.spaceList() {
		// Case-insensitive: on macOS / Windows "abc" and "ABC" would be the same project directory
		id := str(s, "id")
		if strings.EqualFold(id, name) || strings.EqualFold(spaceName(s), name) {
			return errors.New(i18n.S("已经有叫「"+spaceName(s)+"」的项目了（id "+id+"）", "there's already a project named \""+spaceName(s)+"\" (id "+id+")"))
		}
	}
	return nil
}

func (c *layoutCall) projectView(s node) node {
	id := str(s, "id")
	n := 0
	for _, w := range c.wss() {
		if spaceOfWs(w) == id {
			n++
		}
	}
	v := node{"id": id, "name": spaceName(s), "workspace_count": n}
	if c.space != "" && id == c.space {
		v["current"] = true
	}
	return v
}

func (c *layoutCall) projectList() any {
	out := []node{}
	for _, s := range c.spaceList() {
		out = append(out, c.projectView(s))
	}
	return node{"projects": out}
}

// projectNew adds the project. A project that existed before under that name gets its workspaces back from its
// directory (work.Runner.SaveLayouts keeps each project's copy), as when it's re-created in the sidebar; otherwise it
// starts with one empty workspace in cwd. It doesn't switch any window to it.
func (c *layoutCall) projectNew(p LayoutParams) (any, error) {
	id := strings.TrimSpace(p.ID)
	if err := c.projectNameError(id); err != nil {
		return nil, err
	}
	var back []node
	if c.savedLayout != nil {
		if saved, _ := c.savedLayout(id); saved != nil {
			for _, raw := range saved.Workspaces {
				var w node
				if json.Unmarshal(raw, &w) != nil || str(w, "id") == "" || len(tabsOf(w)) == 0 || c.wsByID(str(w, "id")) != nil {
					continue
				}
				w["space"] = id
				back = append(back, w)
			}
		}
	}
	// Listed before its workspaces go in: spaceList would otherwise pick the project up from them as well
	sp := node{"id": id, "name": id}
	c.l["spaces"] = anys(append(c.spaceList(), sp))
	restored := len(back) > 0
	if restored {
		c.setWss(append(c.wss(), back...))
	} else {
		w, _ := c.newWorkspace("default", p.Cwd, id)
		back = []node{w}
	}
	c.changed = true
	ws := []node{}
	for i, w := range back {
		ws = append(ws, c.wsView(w, i+1))
	}
	return node{"project": c.projectView(sp), "workspaces": ws, "restored": restored}, nil
}

func (c *layoutCall) wsByID(id string) node {
	for _, w := range c.wss() {
		if str(w, "id") == id {
			return w
		}
	}
	return nil
}

// LayoutOpts are what some layout methods need from outside the layout: project.new reads a project's saved workspaces.
type LayoutOpts struct {
	SavedLayout func(project string) (*work.ProjectLayout, error)
}
