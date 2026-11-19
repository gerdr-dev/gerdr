package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
)

// fork answers acpx.MethodFork: a copy of the source's transcript under a new session id, next to it, which
// `claude --resume <new id>` then carries on. Not claude's own --fork-session: the copy is made here, line for line,
// so the resumed conversation is the original's exactly (the same prompt prefix, so the prompt cache hits), the
// message uuids stay the same (a rewind in the copy finds them), and nothing about the source changes.
func (a *Agent) fork(p acpx.ForkRequest) (acpx.ForkResponse, error) {
	src := string(p.SessionId)
	path, err := findTranscript(a.projectsDir(), src, p.Cwd)
	if err != nil {
		return acpx.ForkResponse{}, err // nothing written yet (new, or rewound and not spoken since): the client copies the chat instead
	}
	id := uuid.NewString()
	if err := copyTranscript(path, src, id); err != nil {
		return acpx.ForkResponse{}, err
	}
	return acpx.ForkResponse{SessionId: acp.SessionId(id)}, nil
}

// forkDropTypes: transcript lines that name the session rather than carry the conversation. Copied, claude would
// report the original's name for the fork on resume (a /rename even as the user's own), over Gerdr's "Fork: …".
var forkDropTypes = map[string]bool{"custom-title": true, "ai-title": true, "agent-name": true}

// copyTranscript writes path's transcript as session id's, beside it: each line with the session id swapped (only
// that field: the content stays byte for byte), the title lines left out, and the <src>/ folder next to it
// (subagent transcripts, large tool results) copied as <id>/.
func copyTranscript(path, src, id string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	dir := filepath.Dir(path)
	dst := filepath.Join(dir, id+".jsonl")
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // gone after the rename; left behind only on failure
	w := bufio.NewWriter(out)
	from, to := []byte(`"sessionId":"`+src+`"`), []byte(`"sessionId":"`+id+`"`)
	r := bufio.NewReader(in)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var e struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(line, &e) != nil || !forkDropTypes[e.Type] {
				w.Write(bytes.ReplaceAll(line, from, to))
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			out.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := copyDir(filepath.Join(dir, src), filepath.Join(dir, id)); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// copyDir copies a folder's files (none there: nothing to do).
func copyDir(from, to string) error {
	if st, err := os.Stat(from); errors.Is(err, os.ErrNotExist) || err == nil && !st.IsDir() {
		return nil
	}
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
}
