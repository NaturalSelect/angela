package proto

// ReportImportRequest asks the server to store a report read from disk in
// a session. ID is optional: the server keeps it when no different report
// in the session's lineage uses it, and assigns a fresh one otherwise.
type ReportImportRequest struct {
	ID        string `json:"id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Task      string `json:"task,omitempty"`
	Body      string `json:"body"`
}

// ReportImportResult says what an import did. ID is the one LoadReport
// takes; it differs from RequestedID when the requested id already named
// another report.
type ReportImportResult struct {
	ID             string `json:"id"`
	AgentType      string `json:"agent_type"`
	Task           string `json:"task,omitempty"`
	RequestedID    string `json:"requested_id,omitempty"`
	AlreadyPresent bool   `json:"already_present"`
}
