package codex

import "encoding/json"

// 只声明我们读写到的字段。完整定义：`codex app-server generate-ts --out <dir>`。

type thread struct {
	ID        string  `json:"id"`
	Preview   string  `json:"preview"`
	Name      *string `json:"name"`
	Cwd       string  `json:"cwd"`
	Model     *string `json:"model"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
	Turns     []turn  `json:"turns"`
	Ephemeral bool    `json:"ephemeral"`
}

type threadStartResp struct {
	Thread          thread  `json:"thread"`
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoningEffort"`
}

type turn struct {
	ID     string            `json:"id"`
	Items  []json.RawMessage `json:"items"`
	Status string            `json:"status"`
	Error  *turnError        `json:"error"`
}

type turnError struct {
	Message           string  `json:"message"`
	AdditionalDetails *string `json:"additionalDetails"`
}

type turnResp struct {
	Turn turn `json:"turn"`
}

type itemHead struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type userInput struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	TextElements []any  `json:"text_elements,omitempty"`
	URL          string `json:"url,omitempty"`
	Path         string `json:"path,omitempty"`
}

type userMessageItem struct {
	ID       string      `json:"id"`
	ClientID *string     `json:"clientId"`
	Content  []userInput `json:"content"`
}

type asyncQuestion struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}

type agentMessageItem struct {
	ID        string          `json:"id"`
	Text      string          `json:"text"`
	Phase     *string         `json:"phase"`
	Delivery  *string         `json:"delivery"`
	Questions []asyncQuestion `json:"questions"`
}

type reasoningItem struct {
	ID      string   `json:"id"`
	Summary []string `json:"summary"`
	Content []string `json:"content"`
}

type commandAction struct {
	Type    string  `json:"type"`
	Command string  `json:"command"`
	Name    string  `json:"name"`
	Path    *string `json:"path"`
	Query   *string `json:"query"`
}

type commandItem struct {
	ID               string          `json:"id"`
	Command          string          `json:"command"`
	Cwd              string          `json:"cwd"`
	Status           string          `json:"status"`
	CommandActions   []commandAction `json:"commandActions"`
	AggregatedOutput *string         `json:"aggregatedOutput"`
	ExitCode         *int            `json:"exitCode"`
	DurationMs       *int64          `json:"durationMs"`
}

type fileUpdateChange struct {
	Path string `json:"path"`
	Kind struct {
		Type     string  `json:"type"`
		MovePath *string `json:"move_path"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

type fileChangeItem struct {
	ID      string             `json:"id"`
	Changes []fileUpdateChange `json:"changes"`
	Status  string             `json:"status"`
}

type mcpToolCallItem struct {
	ID        string          `json:"id"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Status    string          `json:"status"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`
}

type webSearchItem struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

type planStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type modelInfo struct {
	ID                        string `json:"id"`
	Model                     string `json:"model"`
	DisplayName               string `json:"displayName"`
	Description               string `json:"description"`
	Hidden                    bool   `json:"hidden"`
	IsDefault                 bool   `json:"isDefault"`
	DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort string `json:"reasoningEffort"`
		Description     string `json:"description"`
	} `json:"supportedReasoningEfforts"`
}

type commandApprovalParams struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	ItemID   string          `json:"itemId"`
	Reason   *string         `json:"reason"`
	Command  *string         `json:"command"`
	Cwd      *string         `json:"cwd"`
	Kind     string          `json:"kind"`
	Actions  []commandAction `json:"commandActions"`
}

type fileApprovalParams struct {
	ThreadID  string  `json:"threadId"`
	TurnID    string  `json:"turnId"`
	ItemID    string  `json:"itemId"`
	Reason    *string `json:"reason"`
	GrantRoot *string `json:"grantRoot"`
}

type permissionsApprovalParams struct {
	ThreadID    string          `json:"threadId"`
	TurnID      string          `json:"turnId"`
	ItemID      string          `json:"itemId"`
	Cwd         string          `json:"cwd"`
	Reason      *string         `json:"reason"`
	Permissions json.RawMessage `json:"permissions"`
}

type userInputParams struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Questions []struct {
		ID       string `json:"id"`
		Header   string `json:"header"`
		Question string `json:"question"`
		IsOther  bool   `json:"isOther"`
		IsSecret bool   `json:"isSecret"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}
