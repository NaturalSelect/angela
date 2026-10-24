package protocol

type PermissionKind string

const (
	PermissionKindAllowOnce    PermissionKind = "allow_once"
	PermissionKindAllowAlways  PermissionKind = "allow_always"
	PermissionKindRejectOnce   PermissionKind = "reject_once"
	PermissionKindRejectAlways PermissionKind = "reject_always"
)

type PermissionRequest struct {
	SessionID string
	ToolCall  ToolCallUpdate
	Options   []PermissionOption
}

type PermissionOption struct {
	ID   string
	Name string
	Kind PermissionKind
}

type PermissionResponse struct {
	Cancelled bool
	OptionID  string
}

type FieldKind string

const (
	FieldKindText         FieldKind = "text"
	FieldKindBoolean      FieldKind = "boolean"
	FieldKindSingleSelect FieldKind = "single_select"
	FieldKindMultiSelect  FieldKind = "multi_select"
)

// NOTE: Form mode only. Do not send it unless the editor declared
// elicitation.form.
type ElicitationRequest struct {
	SessionID  string
	ToolCallID string
	Message    string
	Fields     []FormField
}

type FormField struct {
	Name        string
	Title       string
	Description string
	Kind        FieldKind
	Required    bool
	Options     []FormOption
}

type FormOption struct {
	Value string
	Title string
}

type ElicitAction string

const (
	ElicitActionAccept  ElicitAction = "accept"
	ElicitActionDecline ElicitAction = "decline"
	ElicitActionCancel  ElicitAction = "cancel"
)

type ElicitationResponse struct {
	Action ElicitAction
	Values map[string]any
}
