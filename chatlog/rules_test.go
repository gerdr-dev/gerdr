package chatlog

import (
	"testing"

	"github.com/gerdr-dev/gerdr/acpx"
)

// A slow foreground Bash call is tracked as a task by Claude; its tool call row already shows it, so no card.
func TestFinishedTasksSkipsForegroundToolCalls(t *testing.T) {
	prev := []acpx.Task{
		{ID: "fg", ToolCallID: "toolu_1", Status: "running"},
		{ID: "bg", ToolCallID: "toolu_2", Background: true, Status: "running"},
	}
	next := []acpx.Task{
		{ID: "fg", ToolCallID: "toolu_1", Status: "failed"},
		{ID: "bg", ToolCallID: "toolu_2", Background: true, Status: "completed"},
	}
	got := FinishedTasks(prev, next)
	if len(got) != 1 {
		t.Fatalf("want one card (the background task), got %d", len(got))
	}
}
