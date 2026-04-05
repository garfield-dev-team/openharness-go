package types

// UsageSnapshot tracks token usage for an API call.
type UsageSnapshot struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// TotalTokens returns the sum of input and output tokens.
func (u UsageSnapshot) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}
