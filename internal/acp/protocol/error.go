package protocol

type ErrorCode int

const (
	// CodeInvalidParams maps to -32602.
	CodeInvalidParams ErrorCode = iota
	// CodeNotFound maps to -32002 (resource_not_found).
	CodeNotFound
	// CodeInternal maps to -32603.
	CodeInternal
)

// NOTE: Any error that is not an Error is reported as CodeInternal.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e Error) Error() string { return e.Message }
