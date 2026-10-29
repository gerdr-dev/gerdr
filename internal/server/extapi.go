package server

import (
	"net/http"

	"github.com/gerdr-dev/gerdr/internal/ext"
)

// skill / MCP 管理（设置 → Skill / MCP）。agent 那边的只读：列出来给人看、能导入到 gerdr，
// 不往 ~/.claude / ~/.codex 里写任何东西。

type extView struct {
	Settings ext.Settings `json:"settings"`
	Skills   []ext.Skill  `json:"skills"`
	Builtin  []ext.Skill  `json:"builtin"`
	Agent    struct {
		ClaudeSkills  []ext.Skill        `json:"claudeSkills"`
		ClaudePlugins []ext.ClaudePlugin `json:"claudePlugins"`
		ClaudeMCP     []ext.MCPServer    `json:"claudeMCP"`
		CodexSkills   []ext.Skill        `json:"codexSkills"`
		CodexMCP      []ext.MCPServer    `json:"codexMCP"`
	} `json:"agent"`
}

func (s *Server) extRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ext", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.extView())
	})
	mux.HandleFunc("PUT /api/ext/settings", func(w http.ResponseWriter, r *http.Request) {
		var st ext.Settings
		if err := decode(r, &st); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := s.Ext.Save(st); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, s.extView())
	})
	mux.HandleFunc("GET /api/ext/skills/{name}", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.Ext.ReadSkill(r.PathValue("name"))
		if err != nil {
			httpError(w, 404, err.Error())
			return
		}
		writeJSON(w, map[string]string{"content": c})
	})
	mux.HandleFunc("PUT /api/ext/skills/{name}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Content string `json:"content"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := s.Ext.WriteSkill(r.PathValue("name"), b.Content); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, s.extView())
	})
	mux.HandleFunc("DELETE /api/ext/skills/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Ext.DeleteSkill(r.PathValue("name")); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, s.extView())
	})
	// 导入：只认 agent 那边列得出来的目录（按 dir 精确匹配），不接受任意路径。
	mux.HandleFunc("POST /api/ext/skills/import", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Dir string `json:"dir"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		v := s.extView()
		for _, list := range [][]ext.Skill{v.Agent.ClaudeSkills, v.Agent.CodexSkills} {
			for _, sk := range list {
				if sk.Dir != b.Dir {
					continue
				}
				// 插件里的名字带 `插件:` 前缀，落到 gerdr 里用目录名。
				if !ext.ValidName(sk.Name) {
					sk.Name = baseName(sk.Dir)
				}
				if err := s.Ext.ImportSkill(sk); err != nil {
					httpError(w, 400, err.Error())
					return
				}
				writeJSON(w, s.extView())
				return
			}
		}
		httpError(w, 404, "没有这个 skill")
	})
}

func (s *Server) extView() extView {
	var v extView
	v.Settings = s.Ext.Settings()
	v.Skills = nz(s.Ext.Skills())
	v.Builtin = nz(s.Ext.Builtin())
	v.Agent.ClaudeSkills = nz(ext.ClaudeSkills(""))
	v.Agent.ClaudePlugins = nz(ext.ClaudePlugins(""))
	v.Agent.ClaudeMCP = nz(ext.ClaudeMCP(""))
	v.Agent.CodexSkills = nz(ext.CodexSkills())
	v.Agent.CodexMCP = nz(ext.CodexMCP())
	return v
}

func nz[T any](a []T) []T {
	if a == nil {
		return []T{}
	}
	return a
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
