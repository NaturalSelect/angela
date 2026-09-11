package message

// QueuedPrompt is a prompt waiting in a session's turn queue, together
// with the attachments it was submitted with.
type QueuedPrompt struct {
	Prompt      string
	Attachments []Attachment
}
