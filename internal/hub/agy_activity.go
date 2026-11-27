package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/agenthost"
	"github.com/google/uuid"
)

func (s *Session) prepareAgyInference(c *agenthost.Host) {
	info := c.Info.AgentInfo
	agy := s.infersTurns() && info != nil && info.Name == "Antigravity ACP Server"
	var probe func(string) bool
	if agy {
		def, _ := s.h.agentDef(s.rec.Agent)
		probe = agyActivityProbe(def, c.Conn.Done())
	}
	s.mu.Lock()
	s.agyACP, s.agyProbe = agy, probe
	s.mu.Unlock()
}

// Agy 1.5 marks invoke_subagent complete as soon as it has launched a child, and does not stream the child's run
// state over ACP. Its local conversation index does expose that state, including when a child is nominally idle but
// has background Shell work (not_fully_idle). Consult it only after a launch and a quiet interval. A missing or
// incompatible index falls back to the child's Shell records and the usual ACP quiet timer.
func agyActivityProbe(def AgentDef, disconnected <-chan struct{}) func(string) bool {
	for _, arg := range def.Command {
		switch name, _, _ := strings.Cut(arg, "="); name {
		case "--app-data-dir", "--app_data_dir", "--cli_dir_name", "--gemini-dir", "--gemini_dir":
			return nil // configured data directory; the default index may belong to another process
		}
	}
	home := def.Env["HOME"]
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return nil
	}
	sqlite, _ := exec.LookPath("sqlite3")
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	return func(sid string) bool {
		select {
		case <-disconnected:
			return false
		default:
		}
		// ACP IDs are opaque in general; only canonical Agy UUIDs belong in this query.
		id, err := uuid.Parse(sid)
		if err != nil || id.String() != sid {
			return false
		}
		if agyChildHasPendingShell(filepath.Join(root, "brain"), sid) {
			return true
		}
		if sqlite == "" {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		query := "SELECT EXISTS(SELECT 1 FROM conversation_summaries WHERE killed=0 AND not_fully_idle=1" +
			" AND (conversation_id='" + sid + "' OR parent_conversation_id='" + sid + "'));"
		db := filepath.Join(root, "conversation_summaries.db")
		out, err := exec.CommandContext(ctx, sqlite, "-init", os.DevNull, "-readonly", "-batch", "-noheader", db, query).Output()
		return err == nil && strings.TrimSpace(string(out)) == "1"
	}
}

// Agy's index can briefly say idle even while a child has an asynchronous terminal. The child's transcript
// records the launch as RUNNING, and Agy writes the step's output file when that terminal exits. The transcript
// entry itself stays RUNNING. Only inspect child UUIDs linked from this parent, and only the recent transcript.
func agyChildHasPendingShell(brain, parent string) bool {
	children, _ := os.ReadDir(filepath.Join(brain, parent, ".system_generated", "subagents"))
	for _, child := range children {
		id, ok := strings.CutSuffix(child.Name(), ".json")
		parsed, err := uuid.Parse(id)
		if child.IsDir() || !ok || err != nil || parsed.String() != id {
			continue
		}
		if agyPendingShell(filepath.Join(brain, id, ".system_generated")) {
			return true
		}
	}
	return false
}

func agyPendingShell(data string) bool {
	f, err := os.Open(filepath.Join(data, "logs", "transcript.jsonl"))
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	const tail = 256 << 10
	start := max(info.Size()-tail, 0)
	scan := bufio.NewScanner(io.NewSectionReader(f, start, info.Size()-start))
	scan.Buffer(make([]byte, 4096), tail+1)
	if start > 0 {
		scan.Scan() // discard the partially read first line
	}
	for scan.Scan() {
		var step struct {
			Index   *int      `json:"step_index"`
			Status  string    `json:"status"`
			Content string    `json:"content"`
			Created time.Time `json:"created_at"`
		}
		if json.Unmarshal(scan.Bytes(), &step) != nil || step.Index == nil || *step.Index < 0 ||
			step.Status != "RUNNING" || time.Since(step.Created) > inferOpenLimit ||
			!strings.Contains(step.Content, "Tool is running as a background task with task id:") {
			continue
		}
		if _, err := os.Stat(filepath.Join(data, "steps", strconv.Itoa(*step.Index), "output.txt")); errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}
