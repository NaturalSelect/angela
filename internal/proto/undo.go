package proto

// UndoSkippedFile is a file undo left untouched, and why.
type UndoSkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// UndoPreview is what undoing a session's last turn would do, without
// doing it.
type UndoPreview struct {
	// CutMessageID identifies the turn: it is the earliest message
	// Undo would remove, and must be passed back to Undo unchanged.
	CutMessageID string            `json:"cut_message_id"`
	PoppedText   string            `json:"popped_text"`
	MessageCount int               `json:"message_count"`
	Revert       []string          `json:"revert"`
	Delete       []string          `json:"delete"`
	Skipped      []UndoSkippedFile `json:"skipped"`
}

// UndoResult is what undoing a session's last turn actually did.
type UndoResult struct {
	PoppedText   string            `json:"popped_text"`
	Reverted     []string          `json:"reverted"`
	Deleted      []string          `json:"deleted"`
	Skipped      []UndoSkippedFile `json:"skipped"`
	MessageCount int               `json:"message_count"`
}

// UndoRequest asks Undo to revert the turn identified by
// CutMessageID, as returned by a prior UndoPreview for the same
// session.
type UndoRequest struct {
	CutMessageID string `json:"cut_message_id"`
}
