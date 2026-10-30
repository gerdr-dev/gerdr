package hub

import (
	"time"

	"github.com/gerdr-dev/gerdr/chatlog"
)

// The event log's format and file live in the public chatlog package (Annulo records its own sessions with it);
// these aliases keep the hub's code reading as before.

type Event = chatlog.Event

type logFile = chatlog.File

const (
	EvUser           = chatlog.EvUser
	EvUpdate         = chatlog.EvUpdate
	EvTurnStart      = chatlog.EvTurnStart
	EvTurnEnd        = chatlog.EvTurnEnd
	EvPermission     = chatlog.EvPermission
	EvPermissionDone = chatlog.EvPermissionDone
	EvAsk            = chatlog.EvAsk
	EvAskDone        = chatlog.EvAskDone
	EvNotice         = chatlog.EvNotice
	EvSteerState     = chatlog.EvSteerState
	EvTask           = chatlog.EvTask
)

func now() int64 { return time.Now().UnixMilli() }

func readLog(path string) ([]*Event, error) { return chatlog.ReadFile(path) }
