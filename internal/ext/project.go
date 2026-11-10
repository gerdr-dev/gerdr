package ext

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// A Gerdr project's own skills and memory, in <GERDR_DIR>/projects/<id>/{skills,memory}: kept on this computer, given
// only to the sessions of that project (decided with the user, 2026-11). A project spans several repositories, so
// these are the layer the repositories' own files (.claude/skills, CLAUDE.md) and the agents' per-directory memories
// can't be: what holds for the whole project. Living in the project's directory, they go to the trash with it.
//
// How sessions get them: the skills folder is one more of the session's skill roots (hub.Options.SkillDirs →
// _meta skillRoots, like a panel's skills), the memory index is appended to the session's system prompt
// (agentwire.ProjectPrompt).

// OriginProject is a skill of the session's Gerdr project (<GERDR_DIR>/projects/<id>/skills).
const OriginProject = "project"

// ValidProject mirrors work.validProject (ext can't import work): a project id is also a directory name.
func ValidProject(id string) bool {
	if id == "" || utf8.RuneCountInString(id) > 64 || strings.TrimSpace(id) != id {
		return false
	}
	return !strings.ContainsFunc(id, func(c rune) bool { return c == '/' || c == '\\' || c == '.' || c < 0x20 })
}

func (m *Manager) projectDir(project string) (string, error) {
	if !ValidProject(project) {
		return "", fmt.Errorf(i18n.S("项目名 %q 不合法", "invalid project %q"), project)
	}
	return filepath.Join(m.dir, "projects", project), nil
}

// ProjectSkillsDir / ProjectMemoryDir: where a project's skills / memory live (they may not exist yet).
func (m *Manager) ProjectSkillsDir(project string) (string, error) {
	d, err := m.projectDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "skills"), nil
}

func (m *Manager) ProjectMemoryDir(project string) (string, error) {
	d, err := m.projectDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "memory"), nil
}

// ProjectSkills lists a project's own skills. They have no on / off switch: delete one, or switch the name off
// for the project (disabledSkills), which applies to these too.
func (m *Manager) ProjectSkills(project string) []Skill {
	d, err := m.ProjectSkillsDir(project)
	if err != nil {
		return nil
	}
	return scanSkills(d, OriginProject)
}

func (m *Manager) projectSkillPath(project, name string) (string, error) {
	d, err := m.ProjectSkillsDir(project)
	if err != nil {
		return "", err
	}
	if !ValidName(name) {
		return "", fmt.Errorf(i18n.S("skill 名字 %q 不合法：只能用字母、数字、- 和 _", "invalid skill name %q: use only letters, digits, - and _"), name)
	}
	return filepath.Join(d, name, "SKILL.md"), nil
}

func (m *Manager) ReadProjectSkill(project, name string) (string, error) {
	p, err := m.projectSkillPath(project, name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	return string(b), err
}

func (m *Manager) WriteProjectSkill(project, name, content string) error {
	p, err := m.projectSkillPath(project, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func (m *Manager) DeleteProjectSkill(project, name string) error {
	p, err := m.projectSkillPath(project, name)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(p))
}

// projectsSignature: every project's skills (name, size, time), for Signature: open Settings pages refetch after
// an agent or a hand edit adds one.
func (m *Manager) projectsSignature() string {
	h := sha256.New()
	ps, _ := os.ReadDir(filepath.Join(m.dir, "projects"))
	for _, p := range ps {
		root := filepath.Join(m.dir, "projects", p.Name(), "skills")
		ents, _ := os.ReadDir(root)
		for _, e := range ents {
			if st, err := os.Stat(filepath.Join(root, e.Name(), "SKILL.md")); err == nil {
				fmt.Fprintln(h, p.Name(), e.Name(), st.Size(), st.ModTime().UnixNano())
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RootsFingerprint changes when the skills in a session's own skill folders come or go (not when one is edited:
// claude gets them as links and codex reads the files, so an edit is live). Part of the session's fingerprint, so a
// skill added to the project reaches a running session before its next turn, as a settings change does.
func RootsFingerprint(roots []string) string {
	if len(roots) == 0 {
		return ""
	}
	h := sha256.New()
	for _, r := range roots {
		fmt.Fprintln(h, r)
		for _, sk := range scanSkills(r, "") {
			fmt.Fprintln(h, sk.Name, filepath.Base(sk.Dir))
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ScanSkills lists the skills in root (<root>/<name>/SKILL.md), each marked with origin.
func ScanSkills(root, origin string) []Skill { return scanSkills(root, origin) }

// AddProjectSkillDir copies a whole skill folder into a project's skills as name (the CLI's `skill add --dir
// --project`; any local path, as for AddSkillDir).
func (m *Manager) AddProjectSkillDir(project, name, src string, replace bool) error {
	p, err := m.projectSkillPath(project, name)
	if err != nil {
		return err
	}
	return addSkillDir(filepath.Dir(filepath.Dir(p)), name, src, replace)
}
