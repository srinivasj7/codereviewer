package schemas

// RawComment is the wire shape of one comment as emitted by the LLM,
// per Appendix A of the design. Validation lives in internal/core/llm.
type RawComment struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Comment   string `json:"comment"`
	Category  string `json:"category"` // bug | security | style | suggestion | question
	Severity  string `json:"severity"` // high | medium | low
}

// LlmOutput is the top-level JSON array the LLM is asked to emit.
type LlmOutput []RawComment
