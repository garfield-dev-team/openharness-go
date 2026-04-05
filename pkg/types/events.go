package types

// StreamEvent is the interface for all streaming events produced during an
// assistant turn.
type StreamEvent interface {
	streamEvent() // unexported marker method
}

// AssistantTextDelta carries incremental text from the model.
type AssistantTextDelta struct {
	Text string `json:"text"`
}

func (AssistantTextDelta) streamEvent() {}

// AssistantTurnComplete signals the model finished its turn.
type AssistantTurnComplete struct {
	Message ConversationMessage `json:"message"`
	Usage   UsageSnapshot       `json:"usage"`
}

func (AssistantTurnComplete) streamEvent() {}

// ToolExecutionStarted signals a tool call is about to be executed.
type ToolExecutionStarted struct {
	ToolName  string                 `json:"tool_name"`
	ToolInput map[string]interface{} `json:"tool_input"`
}

func (ToolExecutionStarted) streamEvent() {}

// ToolExecutionCompleted signals a tool call has finished.
type ToolExecutionCompleted struct {
	ToolName string `json:"tool_name"`
	Output   string `json:"output"`
	IsError  bool   `json:"is_error"`
}

func (ToolExecutionCompleted) streamEvent() {}
