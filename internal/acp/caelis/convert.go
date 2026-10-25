package caelis

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
)

const (
	codeInvalidParams = -32602
	codeNotFound      = -32002
	codeInternal      = -32603
)

func invalidParams(msg string) error {
	return protocol.Error{Code: protocol.CodeInvalidParams, Message: msg}
}

// NOTE: Anything that is not a protocol.Error is reported as an internal
// error, including context.Canceled.
func toRequestError(err error) error {
	if err == nil {
		return nil
	}
	if pe, ok := errors.AsType[protocol.Error](err); ok {
		code := codeInternal
		switch pe.Code {
		case protocol.CodeInvalidParams:
			code = codeInvalidParams
		case protocol.CodeNotFound:
			code = codeNotFound
		}
		return &acp.RequestError{Code: code, Message: pe.Message}
	}
	return &acp.RequestError{Code: codeInternal, Message: err.Error()}
}

func toInitialize(p acp.InitializeRequest) protocol.InitializeRequest {
	elicitation := p.ClientCapabilities.Elicitation
	return protocol.InitializeRequest{
		ProtocolVersion: int(p.ProtocolVersion),
		ElicitationForm: elicitation != nil && elicitation.Form != nil,
	}
}

func fromInitializeResponse(r protocol.InitializeResponse) acp.InitializeResponse {
	caps := acp.AgentCapabilities{
		LoadSession: r.LoadSession,
		PromptCapabilities: acp.PromptCapabilities{
			Image:           r.PromptImage,
			EmbeddedContext: r.PromptEmbeddedContext,
		},
	}
	if r.ResumeSession {
		caps.SessionCapabilities.Resume = &acp.SessionResumeCapabilities{}
	}
	if r.ListSessions {
		caps.SessionCapabilities.List = &acp.SessionListCapabilities{}
	}
	if r.DeleteSessions {
		caps.SessionCapabilities.Delete = &acp.SessionDeleteCapabilities{}
	}
	if r.CloseSession {
		caps.SessionCapabilities.Close = &acp.SessionCloseCapabilities{}
	}
	return acp.InitializeResponse{
		ProtocolVersion:   acp.ProtocolVersion(r.ProtocolVersion),
		AgentCapabilities: caps,
		AgentInfo:         &acp.Implementation{Name: r.AgentName, Version: r.AgentVersion},
		AuthMethods:       []acp.AuthMethod{},
	}
}

func toNewSession(p acp.NewSessionRequest) protocol.NewSessionRequest {
	return protocol.NewSessionRequest{Cwd: p.Cwd}
}

func fromNewSession(r protocol.NewSessionResponse) acp.NewSessionResponse {
	return acp.NewSessionResponse{
		SessionId:     acp.SessionId(r.SessionID),
		ConfigOptions: fromConfigOptions(r.ConfigOptions),
	}
}

func toLoadSession(p acp.LoadSessionRequest) protocol.LoadSessionRequest {
	return protocol.LoadSessionRequest{SessionID: string(p.SessionId), Cwd: p.Cwd}
}

func fromLoadSession(r protocol.LoadSessionResponse) acp.LoadSessionResponse {
	return acp.LoadSessionResponse{ConfigOptions: fromConfigOptions(r.ConfigOptions)}
}

func toResumeSession(p acp.ResumeSessionRequest) protocol.ResumeSessionRequest {
	return protocol.ResumeSessionRequest{SessionID: string(p.SessionId), Cwd: p.Cwd}
}

func fromResumeSession(r protocol.ResumeSessionResponse) acp.ResumeSessionResponse {
	return acp.ResumeSessionResponse{ConfigOptions: fromConfigOptions(r.ConfigOptions)}
}

func toListSessions(p acp.ListSessionsRequest) protocol.ListSessionsRequest {
	return protocol.ListSessionsRequest{Cwd: deref(p.Cwd), Cursor: deref(p.Cursor)}
}

func fromListSessions(r protocol.ListSessionsResponse) acp.ListSessionsResponse {
	sessions := make([]acp.SessionInfo, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		info := acp.SessionInfo{SessionId: acp.SessionId(s.SessionID), Cwd: s.Cwd}
		if s.Title != "" {
			info.Title = &s.Title
		}
		if !s.UpdatedAt.IsZero() {
			updated := s.UpdatedAt.UTC().Format(time.RFC3339)
			info.UpdatedAt = &updated
		}
		sessions = append(sessions, info)
	}
	resp := acp.ListSessionsResponse{Sessions: sessions}
	if r.NextCursor != "" {
		resp.NextCursor = &r.NextCursor
	}
	return resp
}

func toDeleteSession(p acp.DeleteSessionRequest) protocol.DeleteSessionRequest {
	return protocol.DeleteSessionRequest{SessionID: string(p.SessionId)}
}

func toCloseSession(p acp.CloseSessionRequest) protocol.CloseSessionRequest {
	return protocol.CloseSessionRequest{SessionID: string(p.SessionId)}
}

func toCancel(p acp.CancelNotification) protocol.CancelRequest {
	return protocol.CancelRequest{SessionID: string(p.SessionId)}
}

func toSetConfigOption(p acp.SetSessionConfigOptionRequest) (protocol.SetConfigOptionRequest, error) {
	if p.ValueId == nil {
		return protocol.SetConfigOptionRequest{}, invalidParams("only select config options are supported")
	}
	return protocol.SetConfigOptionRequest{
		SessionID: string(p.ValueId.SessionId),
		ConfigID:  string(p.ValueId.ConfigId),
		Value:     string(p.ValueId.Value),
	}, nil
}

func fromSetConfigOption(r protocol.SetConfigOptionResponse) acp.SetSessionConfigOptionResponse {
	return acp.SetSessionConfigOptionResponse{ConfigOptions: fromConfigOptions(r.ConfigOptions)}
}

func fromConfigOptions(opts []protocol.ConfigOption) []acp.SessionConfigOption {
	out := make([]acp.SessionConfigOption, 0, len(opts))
	for _, o := range opts {
		values := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(o.Values))
		for _, v := range o.Values {
			value := acp.SessionConfigSelectOption{
				Value: acp.SessionConfigValueId(v.Value),
				Name:  v.Name,
			}
			if v.Description != "" {
				value.Description = &v.Description
			}
			values = append(values, value)
		}
		opt := acp.NewSessionConfigOptionSelect(
			acp.SessionConfigValueId(o.CurrentValue),
			acp.SessionConfigSelectOptions{Ungrouped: &values},
		)
		opt.Select.Id = acp.SessionConfigId(o.ID)
		opt.Select.Name = o.Name
		if o.Description != "" {
			opt.Select.Description = &o.Description
		}
		if o.Category != "" {
			category := acp.SessionConfigOptionCategory(o.Category)
			opt.Select.Category = &category
		}
		out = append(out, opt)
	}
	return out
}

func toPrompt(p acp.PromptRequest) (protocol.PromptRequest, error) {
	blocks := make([]protocol.ContentBlock, 0, len(p.Prompt))
	for _, b := range p.Prompt {
		block, err := toContentBlock(b)
		if err != nil {
			return protocol.PromptRequest{}, err
		}
		blocks = append(blocks, block)
	}
	return protocol.PromptRequest{SessionID: string(p.SessionId), Prompt: blocks}, nil
}

func fromPromptResponse(r protocol.PromptResponse) acp.PromptResponse {
	return acp.PromptResponse{StopReason: acp.StopReason(r.StopReason)}
}

func toContentBlock(b acp.ContentBlock) (protocol.ContentBlock, error) {
	switch {
	case b.Text != nil:
		return protocol.TextBlock{Text: b.Text.Text}, nil
	case b.Image != nil:
		data, err := base64.StdEncoding.DecodeString(b.Image.Data)
		if err != nil {
			return nil, invalidParams("image data is not valid base64")
		}
		return protocol.ImageBlock{MIMEType: b.Image.MimeType, Data: data}, nil
	case b.ResourceLink != nil:
		link := b.ResourceLink
		return protocol.ResourceLinkBlock{
			URI:      link.Uri,
			Name:     link.Name,
			MIMEType: deref(link.MimeType),
		}, nil
	case b.Resource != nil:
		return toResourceBlock(b.Resource.Resource)
	case b.Audio != nil:
		return nil, invalidParams("audio content is not supported")
	default:
		return nil, invalidParams("unsupported content block")
	}
}

func toResourceBlock(r acp.EmbeddedResourceResource) (protocol.ContentBlock, error) {
	switch {
	case r.TextResourceContents != nil:
		t := r.TextResourceContents
		return protocol.ResourceBlock{URI: t.Uri, MIMEType: deref(t.MimeType), Text: t.Text}, nil
	case r.BlobResourceContents != nil:
		b := r.BlobResourceContents
		blob, err := base64.StdEncoding.DecodeString(b.Blob)
		if err != nil {
			return nil, invalidParams("resource blob is not valid base64")
		}
		return protocol.ResourceBlock{URI: b.Uri, MIMEType: deref(b.MimeType), Blob: blob}, nil
	default:
		return nil, invalidParams("embedded resource has no contents")
	}
}

func fromContentBlock(b protocol.ContentBlock) (acp.ContentBlock, error) {
	switch b := b.(type) {
	case protocol.TextBlock:
		return acp.TextBlock(b.Text), nil
	case protocol.ImageBlock:
		return acp.ImageBlock(base64.StdEncoding.EncodeToString(b.Data), b.MIMEType), nil
	case protocol.ResourceLinkBlock:
		block := acp.ResourceLinkBlock(b.Name, b.URI)
		if b.MIMEType != "" {
			block.ResourceLink.MimeType = &b.MIMEType
		}
		return block, nil
	case protocol.ResourceBlock:
		var mime *string
		if b.MIMEType != "" {
			mime = &b.MIMEType
		}
		if len(b.Blob) > 0 {
			return acp.ResourceBlock(acp.EmbeddedResourceResource{
				BlobResourceContents: &acp.BlobResourceContents{
					Uri:      b.URI,
					MimeType: mime,
					Blob:     base64.StdEncoding.EncodeToString(b.Blob),
				},
			}), nil
		}
		return acp.ResourceBlock(acp.EmbeddedResourceResource{
			TextResourceContents: &acp.TextResourceContents{Uri: b.URI, MimeType: mime, Text: b.Text},
		}), nil
	default:
		return acp.ContentBlock{}, fmt.Errorf("unsupported content block %T", b)
	}
}

func fromSessionNotification(n protocol.SessionNotification) (acp.SessionNotification, error) {
	update, err := fromSessionUpdate(n.Update)
	if err != nil {
		return acp.SessionNotification{}, err
	}
	return acp.SessionNotification{SessionId: acp.SessionId(n.SessionID), Update: update}, nil
}

func fromSessionUpdate(u protocol.SessionUpdate) (acp.SessionUpdate, error) {
	switch u := u.(type) {
	case protocol.UserMessageChunk:
		content, err := fromContentBlock(u.Content)
		if err != nil {
			return acp.SessionUpdate{}, err
		}
		return acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{
			SessionUpdate: "user_message_chunk",
			Content:       content,
			MessageId:     messageID(u.MessageID),
		}}, nil
	case protocol.AgentMessageChunk:
		content, err := fromContentBlock(u.Content)
		if err != nil {
			return acp.SessionUpdate{}, err
		}
		return acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
			SessionUpdate: "agent_message_chunk",
			Content:       content,
			MessageId:     messageID(u.MessageID),
		}}, nil
	case protocol.AgentThoughtChunk:
		content, err := fromContentBlock(u.Content)
		if err != nil {
			return acp.SessionUpdate{}, err
		}
		return acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
			SessionUpdate: "agent_thought_chunk",
			Content:       content,
			MessageId:     messageID(u.MessageID),
		}}, nil
	case protocol.ToolCallStart:
		return fromToolCallStart(u)
	case protocol.ToolCallUpdate:
		return fromToolCallUpdate(u)
	case protocol.Plan:
		entries := make([]acp.PlanEntry, 0, len(u.Entries))
		for _, e := range u.Entries {
			entries = append(entries, acp.PlanEntry{
				Content:  e.Content,
				Priority: acp.PlanEntryPriority(e.Priority),
				Status:   acp.PlanEntryStatus(e.Status),
			})
		}
		return acp.SessionUpdate{Plan: &acp.SessionUpdatePlan{
			SessionUpdate: "plan",
			Entries:       entries,
		}}, nil
	case protocol.SessionInfoUpdate:
		info := &acp.SessionSessionInfoUpdate{SessionUpdate: "session_info_update"}
		if u.Title != nil {
			info.SetTitle(*u.Title)
		}
		return acp.SessionUpdate{SessionInfoUpdate: info}, nil
	case protocol.UsageUpdate:
		usage := &acp.SessionUsageUpdate{
			SessionUpdate: "usage_update",
			Used:          nonNegative(u.Used),
			Size:          nonNegative(u.Size),
		}
		if u.Cost != nil {
			usage.Cost = &acp.Cost{Amount: u.Cost.Amount, Currency: u.Cost.Currency}
		}
		return acp.SessionUpdate{UsageUpdate: usage}, nil
	case protocol.ConfigOptionUpdate:
		return acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: fromConfigOptions(u.ConfigOptions),
		}}, nil
	case protocol.AvailableCommandsUpdate:
		return acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
			SessionUpdate:     "available_commands_update",
			AvailableCommands: fromAvailableCommands(u.Commands),
		}}, nil
	default:
		return acp.SessionUpdate{}, fmt.Errorf("unsupported session update %T", u)
	}
}

func fromAvailableCommands(cmds []protocol.AvailableCommand) []acp.AvailableCommand {
	out := make([]acp.AvailableCommand, 0, len(cmds))
	for _, c := range cmds {
		cmd := acp.AvailableCommand{Name: c.Name, Description: c.Description}
		if c.InputHint != "" {
			cmd.Input = &acp.AvailableCommandInput{
				Unstructured: &acp.UnstructuredCommandInput{Hint: c.InputHint},
			}
		}
		out = append(out, cmd)
	}
	return out
}

// NOTE: The SDK's start-tool-call helpers fill rawInput from a single
// location, so the struct is built directly. rawInput is only sent when the
// input is complete.
func fromToolCallStart(s protocol.ToolCallStart) (acp.SessionUpdate, error) {
	call := &acp.SessionUpdateToolCall{
		SessionUpdate: "tool_call",
		ToolCallId:    acp.ToolCallId(s.ID),
		Title:         s.Title,
		Kind:          acp.ToolKind(s.Kind),
		Status:        acp.ToolCallStatus(s.Status),
		Locations:     fromLocations(s.Locations),
	}
	if s.Name != "" {
		call.Name = &s.Name
	}
	if len(s.RawInput) > 0 {
		call.RawInput = s.RawInput
	}
	return acp.SessionUpdate{ToolCall: call}, nil
}

func fromToolCallUpdate(u protocol.ToolCallUpdate) (acp.SessionUpdate, error) {
	content, err := fromToolCallContents(u.Content)
	if err != nil {
		return acp.SessionUpdate{}, err
	}
	update := &acp.SessionToolCallUpdate{
		SessionUpdate: "tool_call_update",
		ToolCallId:    acp.ToolCallId(u.ID),
		Title:         u.Title,
		Content:       content,
		Locations:     fromLocations(u.Locations),
	}
	if u.Status != nil {
		status := acp.ToolCallStatus(*u.Status)
		update.Status = &status
	}
	return acp.SessionUpdate{ToolCallUpdate: update}, nil
}

func fromToolCallContents(contents []protocol.ToolCallContent) ([]acp.ToolCallContent, error) {
	if len(contents) == 0 {
		return nil, nil
	}
	out := make([]acp.ToolCallContent, 0, len(contents))
	for _, c := range contents {
		switch c := c.(type) {
		case protocol.ToolContent:
			block, err := fromContentBlock(c.Block)
			if err != nil {
				return nil, err
			}
			out = append(out, acp.ToolContent(block))
		case protocol.ToolDiff:
			if c.OldText != nil {
				out = append(out, acp.ToolDiffContent(c.Path, c.NewText, *c.OldText))
			} else {
				out = append(out, acp.ToolDiffContent(c.Path, c.NewText))
			}
		default:
			return nil, fmt.Errorf("unsupported tool call content %T", c)
		}
	}
	return out, nil
}

func fromLocations(locs []protocol.Location) []acp.ToolCallLocation {
	if len(locs) == 0 {
		return nil
	}
	out := make([]acp.ToolCallLocation, 0, len(locs))
	for _, l := range locs {
		out = append(out, acp.ToolCallLocation{Path: l.Path, Line: l.Line})
	}
	return out
}

func fromPermissionRequest(r protocol.PermissionRequest) (acp.RequestPermissionRequest, error) {
	content, err := fromToolCallContents(r.ToolCall.Content)
	if err != nil {
		return acp.RequestPermissionRequest{}, err
	}
	call := acp.ToolCallUpdate{
		ToolCallId: acp.ToolCallId(r.ToolCall.ID),
		Title:      r.ToolCall.Title,
		Content:    content,
		Locations:  fromLocations(r.ToolCall.Locations),
	}
	if r.ToolCall.Status != nil {
		status := acp.ToolCallStatus(*r.ToolCall.Status)
		call.Status = &status
	}
	options := make([]acp.PermissionOption, 0, len(r.Options))
	for _, o := range r.Options {
		options = append(options, acp.PermissionOption{
			OptionId: acp.PermissionOptionId(o.ID),
			Name:     o.Name,
			Kind:     acp.PermissionOptionKind(o.Kind),
		})
	}
	return acp.RequestPermissionRequest{
		SessionId: acp.SessionId(r.SessionID),
		ToolCall:  call,
		Options:   options,
	}, nil
}

func toPermissionResponse(r acp.RequestPermissionResponse) protocol.PermissionResponse {
	if r.Outcome.Selected != nil {
		return protocol.PermissionResponse{OptionID: string(r.Outcome.Selected.OptionId)}
	}
	return protocol.PermissionResponse{Cancelled: true}
}

func fromElicitationRequest(r protocol.ElicitationRequest) acp.CreateElicitationRequest {
	properties := make(map[string]any, len(r.Fields))
	var required []string
	for _, f := range r.Fields {
		properties[f.Name] = fieldSchema(f)
		if f.Required {
			required = append(required, f.Name)
		}
	}
	form := &acp.CreateElicitationForm{
		Mode:    "form",
		Message: r.Message,
		RequestedSchema: acp.ElicitationSchema{
			Type:       acp.ElicitationSchemaTypeObject,
			Properties: properties,
			Required:   required,
		},
	}
	if r.SessionID != "" {
		id := acp.SessionId(r.SessionID)
		form.SessionId = &id
	}
	if r.ToolCallID != "" {
		id := acp.ToolCallId(r.ToolCallID)
		form.ToolCallId = &id
	}
	return acp.CreateElicitationRequest{Form: form}
}

func fieldSchema(f protocol.FormField) map[string]any {
	schema := map[string]any{}
	if f.Title != "" {
		schema["title"] = f.Title
	}
	if f.Description != "" {
		schema["description"] = f.Description
	}
	switch f.Kind {
	case protocol.FieldKindBoolean:
		schema["type"] = "boolean"
	case protocol.FieldKindSingleSelect:
		schema["type"] = "string"
		schema["oneOf"] = selectOptions(f.Options)
	case protocol.FieldKindMultiSelect:
		schema["type"] = "array"
		schema["items"] = map[string]any{"anyOf": selectOptions(f.Options)}
	default:
		schema["type"] = "string"
	}
	return schema
}

func selectOptions(opts []protocol.FormOption) []map[string]any {
	out := make([]map[string]any, 0, len(opts))
	for _, o := range opts {
		out = append(out, map[string]any{"const": o.Value, "title": o.Title})
	}
	return out
}

func toElicitationResponse(r acp.CreateElicitationResponse) protocol.ElicitationResponse {
	switch {
	case r.Accept != nil:
		return protocol.ElicitationResponse{Action: protocol.ElicitActionAccept, Values: r.Accept.Content}
	case r.Decline != nil:
		return protocol.ElicitationResponse{Action: protocol.ElicitActionDecline}
	default:
		return protocol.ElicitationResponse{Action: protocol.ElicitActionCancel}
	}
}

func messageID(id string) *acp.MessageId {
	if id == "" {
		return nil
	}
	m := acp.MessageId(id)
	return &m
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonNegative(n int64) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n)
}
