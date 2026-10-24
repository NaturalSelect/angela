package protocol

type PromptRequest struct {
	SessionID string
	Prompt    []ContentBlock
}

type PromptResponse struct {
	StopReason StopReason
}

type StopReason string

const (
	StopReasonEndTurn         StopReason = "end_turn"
	StopReasonMaxTokens       StopReason = "max_tokens"
	StopReasonMaxTurnRequests StopReason = "max_turn_requests"
	StopReasonRefusal         StopReason = "refusal"
	StopReasonCancelled       StopReason = "cancelled"
)

// NOTE: Closed union; only the block types in this file implement it.
type ContentBlock interface{ isContentBlock() }

type TextBlock struct {
	Text string
}

type ImageBlock struct {
	MIMEType string
	Data     []byte
}

type ResourceBlock struct {
	URI      string
	MIMEType string
	Text     string
	Blob     []byte
}

type ResourceLinkBlock struct {
	URI      string
	Name     string
	MIMEType string
}

func (TextBlock) isContentBlock()         {}
func (ImageBlock) isContentBlock()        {}
func (ResourceBlock) isContentBlock()     {}
func (ResourceLinkBlock) isContentBlock() {}
