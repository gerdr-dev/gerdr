package native

import (
	"context"
	"os"
	"strings"
)

// Options configures an Agent. The zero value works: models come from Config, transcripts go to Dir, and
// everything gerdr-specific (its skill and MCP settings, MCP sign-in, the login shell's PATH) is off unless
// injected. gerdr itself injects its implementations in internal/agentwire.
type Options struct {
	// Dir holds the transcripts (<Dir>/sessions/<id>.jsonl) and long-term memory (<Dir>/memory).
	Dir string
	// Config is the model configuration, read fresh before every model call (a change applies on the next turn).
	Config func() Config
	// Prompt is appended to the end of the system prompt (a role's extra instructions in gerdr).
	Prompt string

	// SystemPrompt replaces the built-in base prompt (and gerdr's GUI hint) when set. Called before every model
	// call; meta is the `_meta` the client sent with session/new (or the last session/resume / session/load).
	// The environment section and, unless turned off below, instruction files, skills and memory still follow it.
	SystemPrompt func(sessionID, cwd string, meta map[string]any) string
	// NoInstructionFiles: don't read ~/.claude/CLAUDE.md, ~/.codex/AGENTS.md and the AGENTS.md / CLAUDE.md files
	// from the git root down to the cwd into the system prompt.
	NoInstructionFiles bool
	// NoMemory turns long-term memory off: no memory section in the prompt, no approval-free writes to the
	// memory directories, no /memory command.
	NoMemory bool

	// Skills lists the skills offered to the model (name, description, where SKILL.md is), and as /<name>
	// commands. nil = none.
	Skills func(cwd string) []Skill
	// Tools are extra tools implemented in Go, offered next to the built-in ones (sub-agents get them too).
	Tools []Tool
	// MCP lists the MCP servers a session connects to, besides the ones the client passes in session/new's
	// mcpServers. nil = only those.
	MCP func(cwd string) []MCPServer
	// MCPAuth supplies OAuth tokens for remote MCP servers that have no Authorization header. nil = no sign-in:
	// a server answering 401 is reported as not connected.
	MCPAuth MCPAuth

	// Env is the environment for child processes (bash and stdio MCP servers), with extra ("K=V") on top.
	// nil = this process's environment.
	Env func(extra ...string) []string
	// BashEnv adds variables to bash commands ("K=V"), per session. meta as in SystemPrompt.
	BashEnv func(sessionID string, meta map[string]any) []string
	// SafeCommand reports bash commands that run without approval in every mode (on top of the built-in
	// read-only list).
	SafeCommand func(cmd string) bool

	// Files, when set, takes over read / write / edit for the paths it claims (Files.Owns).
	Files Files
	// Images limits images on their way to the model (pasted by the user, read from disk, returned by tools).
	// Zero = send them as they are.
	Images ImageLimits
}

// Skill is a packaged set of instructions the model reads on demand.
type Skill struct {
	Name        string
	Description string
	Dir         string // the directory holding SKILL.md; paths inside the skill are relative to it
}

// MCPServer is one MCP server to connect to. Type: stdio (default) | http (streamable HTTP) | sse (legacy SSE).
// ${VAR} in Command, Args, Env values, URL and header values is expanded from the environment.
type MCPServer struct {
	Name    string            `json:"name"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (s MCPServer) remote() bool {
	return s.Type == "http" || s.Type == "sse" || (s.Command == "" && s.URL != "")
}

// MCPAuth hands out bearer tokens for remote MCP servers. Token is asked before every request ("" = none), so a
// refresh or a fresh sign-in takes effect without reconnecting; Invalidate is called after a 401.
type MCPAuth interface {
	Token(ctx context.Context, name, url string, headers map[string]string) string
	Invalidate(name, url string)
}

// Tool is a tool implemented by the caller.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema of the arguments (an object)
	// ReadOnly tools never ask for approval and may run in parallel with other read-only calls. The others ask
	// in the Ask and Accept-edits modes (the user can allow the tool for the rest of the session) and are refused
	// in Read-only mode.
	ReadOnly bool
	// Title is the tool call's title in the UI; nil = the tool name.
	Title func(args map[string]any) string
	// Run executes one call. ctx is cancelled when the turn is (session/cancel), so a call that waits for the
	// user must select on it. An error is shown to the model as a failed call.
	Run func(ctx context.Context, call ToolCall) (ToolResult, error)
}

// ToolCall is one call of a caller-implemented tool.
type ToolCall struct {
	SessionID  string
	ToolCallID string
	Cwd        string
	Meta       map[string]any // session/new's _meta (see Options.SystemPrompt)
	Subagent   bool           // called by a sub-agent (which has no user to talk to)
	Args       map[string]any
}

// ToolResult is what the model gets back.
type ToolResult struct {
	Text   string
	Images []Image // dropped if the model can't view images
}

// Image is raw image bytes (not base64).
type Image struct {
	MIME string
	Data []byte
}

// Files takes over the file tools for some paths, e.g. a working directory kept in object storage.
// Paths are absolute and cleaned. Version is an opaque token: Write gets the version the model last read
// (or "" for a file it hasn't read) and should fail rather than overwrite a newer one.
type Files interface {
	Owns(path string) bool
	Read(ctx context.Context, path string) (data []byte, version string, err error)
	Write(ctx context.Context, path string, data []byte, version string) error
}

// ImageLimits: images whose long edge is over MaxEdge pixels are scaled down; ones still over MaxBytes are
// re-encoded as JPEG. Zero fields don't limit.
type ImageLimits struct {
	MaxEdge  int
	MaxBytes int
}

// ---- defaults ----

func (a *Agent) childEnv(extra ...string) []string {
	if a.opt.Env != nil {
		return a.opt.Env(extra...)
	}
	return mergeEnv(os.Environ(), extra)
}

// mergeEnv: base with extra on top (a later K=V replaces an earlier one).
func mergeEnv(base, extra []string) []string {
	idx := map[string]int{}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range append(append([]string(nil), base...), extra...) {
		k, _, _ := strings.Cut(kv, "=")
		if i, ok := idx[k]; ok {
			out[i] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	return out
}

func (a *Agent) skills(cwd string) []Skill {
	if a.opt.Skills == nil {
		return nil
	}
	return a.opt.Skills(cwd)
}
