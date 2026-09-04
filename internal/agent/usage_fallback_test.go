package agent

import (
	"errors"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestUsageIsZero(t *testing.T) {
	t.Parallel()

	require.True(t, usageIsZero(fantasy.Usage{}))
	require.False(t, usageIsZero(fantasy.Usage{InputTokens: 1}))
	require.False(t, usageIsZero(fantasy.Usage{OutputTokens: 1}))
	require.False(t, usageIsZero(fantasy.Usage{TotalTokens: 1}))
	require.False(t, usageIsZero(fantasy.Usage{ReasoningTokens: 1}))
	require.False(t, usageIsZero(fantasy.Usage{CacheCreationTokens: 1}))
	require.False(t, usageIsZero(fantasy.Usage{CacheReadTokens: 1}))
}

func TestFallbackStepUsageKeepsProviderUsage(t *testing.T) {
	t.Parallel()

	usage := fantasy.Usage{
		InputTokens:  10,
		OutputTokens: 5,
		TotalTokens:  15,
	}
	step := fantasy.StepResult{
		Response: fantasy.Response{Usage: usage},
	}

	fallbackUsage, estimated := fallbackStepUsage(nil, step)
	require.False(t, estimated)
	require.Equal(t, usage, fallbackUsage)
}

func TestFallbackStepUsageEstimatesPromptAndAssistantText(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{
		fantasy.NewUserMessage("please explain the implementation details"),
	}
	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "the implementation stores state safely"},
			},
		},
	}

	usage, estimated := fallbackStepUsage(messages, step)
	require.True(t, estimated)
	require.Positive(t, usage.InputTokens)
	require.Positive(t, usage.OutputTokens)
	require.Equal(t, usage.InputTokens+usage.OutputTokens, usage.TotalTokens)
}

func TestFallbackStepUsageEstimatesReasoning(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{
		{
			Role: fantasy.MessageRoleAssistant,
			Content: []fantasy.MessagePart{
				fantasy.ReasoningPart{Text: "first reason about the request"},
			},
		},
	}
	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ReasoningContent{Text: "second reason about the answer"},
			},
		},
	}

	usage, estimated := fallbackStepUsage(messages, step)
	require.True(t, estimated)
	require.Positive(t, usage.InputTokens)
	require.Positive(t, usage.OutputTokens)
}

func TestFallbackStepUsageEstimatesToolCalls(t *testing.T) {
	t.Parallel()

	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{
					ToolCallID: "tool-call-1",
					ToolName:   toolnames.View,
					Input:      `{"file_path":"/tmp/example.go"}`,
				},
			},
		},
	}

	usage, estimated := fallbackStepUsage(nil, step)
	require.True(t, estimated)
	require.Zero(t, usage.InputTokens)
	require.Positive(t, usage.OutputTokens)
	require.Equal(t, usage.OutputTokens, usage.TotalTokens)
}

func TestFallbackStepUsageEstimatesToolResults(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{
		{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "tool-call-1",
					Output: fantasy.ToolResultOutputContentText{
						Text: "file contents returned by the tool",
					},
				},
				fantasy.ToolResultPart{
					ToolCallID: "tool-call-2",
					Output: fantasy.ToolResultOutputContentError{
						Error: errors.New("permission denied"),
					},
				},
				fantasy.ToolResultPart{
					ToolCallID: "tool-call-3",
					Output: fantasy.ToolResultOutputContentMedia{
						MediaType: "image/png",
						Text:      "screenshot",
						Data:      "abc123",
					},
				},
			},
		},
	}

	usage, estimated := fallbackStepUsage(messages, fantasy.StepResult{})
	require.True(t, estimated)
	require.Positive(t, usage.InputTokens)
	require.Zero(t, usage.OutputTokens)
	require.Equal(t, usage.InputTokens, usage.TotalTokens)
}

func TestFallbackStepUsageSkipsClientToolResultsAsOutput(t *testing.T) {
	t.Parallel()

	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolResultContent{
					ToolCallID: "tool-call-1",
					ToolName:   toolnames.Bash,
					Result: fantasy.ToolResultOutputContentText{
						Text: "large client-executed payload that should not count as model output tokens",
					},
				},
			},
		},
	}

	usage, estimated := fallbackStepUsage(nil, step)
	require.False(t, estimated)
	require.Zero(t, usage.OutputTokens)
}

func TestFallbackStepUsageCountsProviderToolResultsAsOutput(t *testing.T) {
	t.Parallel()

	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolResultContent{
					ToolCallID:       "tool-call-1",
					ToolName:         toolnames.WebSearch,
					ProviderExecuted: true,
					ClientMetadata:   "provider metadata",
					Result:           fantasy.ToolResultOutputContentText{Text: "provider-executed result"},
				},
			},
		},
	}

	usage, estimated := fallbackStepUsage(nil, step)
	require.True(t, estimated)
	require.Positive(t, usage.OutputTokens)
	require.Equal(t, usage.OutputTokens, usage.TotalTokens)
}

func TestFallbackStepUsageReturnsZeroWithoutContent(t *testing.T) {
	t.Parallel()

	usage, estimated := fallbackStepUsage(nil, fantasy.StepResult{})
	require.False(t, estimated)
	require.True(t, usageIsZero(usage))
}

func TestUpdateSessionUsageSkipsEstimatedCost(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	currentSession := &session.Session{ID: "session-id", Cost: 1.25}
	model := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CostPer1MIn: 10, CostPer1MOut: 20}}}
	usage := fantasy.Usage{InputTokens: 1000, OutputTokens: 2000}

	agent.updateSessionUsage(model, currentSession, usage, nil, true)

	require.Equal(t, 1.25, currentSession.Cost)
	require.Equal(t, int64(1000), currentSession.PromptTokens)
	require.Equal(t, int64(2000), currentSession.CompletionTokens)
	require.True(t, currentSession.EstimatedUsage)
}

func TestUpdateSessionUsageKeepsCountersForZeroUsage(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	currentSession := &session.Session{
		ID:               "session-id",
		PromptTokens:     123,
		CompletionTokens: 456,
		Cost:             1.25,
	}
	model := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CostPer1MIn: 10, CostPer1MOut: 20}}}

	agent.updateSessionUsage(model, currentSession, fantasy.Usage{}, nil, false)

	require.Equal(t, 1.25, currentSession.Cost)
	require.Equal(t, int64(123), currentSession.PromptTokens)
	require.Equal(t, int64(456), currentSession.CompletionTokens)
}

func TestUpdateSessionUsagePreservesOmittedCountersForPartialUsage(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	currentSession := &session.Session{
		ID:               "session-id",
		PromptTokens:     123,
		CompletionTokens: 456,
	}
	model := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CostPer1MIn: 10, CostPer1MOut: 20}}}
	usage := fantasy.Usage{InputTokens: 789}

	agent.updateSessionUsage(model, currentSession, usage, nil, false)

	require.Equal(t, int64(789), currentSession.PromptTokens)
	require.Equal(t, int64(456), currentSession.CompletionTokens)
}

func TestUpdateSessionUsagePreservesCountersForTotalOnlyUsage(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	currentSession := &session.Session{
		ID:               "session-id",
		PromptTokens:     123,
		CompletionTokens: 456,
	}
	model := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CostPer1MIn: 10, CostPer1MOut: 20}}}
	usage := fantasy.Usage{TotalTokens: 100}

	agent.updateSessionUsage(model, currentSession, usage, nil, false)

	require.Equal(t, int64(123), currentSession.PromptTokens)
	require.Equal(t, int64(456), currentSession.CompletionTokens)
}

func TestUpdateSessionUsagePreservesPromptForOutputOnlyUsage(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	currentSession := &session.Session{
		ID:               "session-id",
		PromptTokens:     123,
		CompletionTokens: 456,
	}
	model := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CostPer1MIn: 10, CostPer1MOut: 20}}}
	usage := fantasy.Usage{OutputTokens: 50}

	agent.updateSessionUsage(model, currentSession, usage, nil, false)

	require.Equal(t, int64(123), currentSession.PromptTokens)
	require.Equal(t, int64(50), currentSession.CompletionTokens)
}

func TestUpdateSessionUsageKeepsCountersForEstimatedZeroUsage(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	currentSession := &session.Session{
		ID:               "session-id",
		PromptTokens:     123,
		CompletionTokens: 456,
		Cost:             1.25,
	}
	model := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CostPer1MIn: 10, CostPer1MOut: 20}}}

	agent.updateSessionUsage(model, currentSession, fantasy.Usage{}, nil, true)

	require.Equal(t, 1.25, currentSession.Cost)
	require.Equal(t, int64(123), currentSession.PromptTokens)
	require.Equal(t, int64(456), currentSession.CompletionTokens)
}

func TestSummaryCompletionTokens(t *testing.T) {
	t.Parallel()

	summaryMessage := message.Message{
		Parts: []message.ContentPart{
			message.TextContent{Text: "summary text"},
			message.ReasoningContent{Thinking: "reasoning text"},
		},
	}

	require.Equal(t, int64(42), summaryCompletionTokens(fantasy.Usage{OutputTokens: 42}, summaryMessage))
	require.Equal(t, approxTokenCount("summary text")+approxTokenCount("reasoning text"), summaryCompletionTokens(fantasy.Usage{}, summaryMessage))
	require.Zero(t, summaryCompletionTokens(fantasy.Usage{}, message.Message{}))
}

func TestUpdateSessionUsageAddsProviderCost(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	currentSession := &session.Session{ID: "session-id", Cost: 1.25}
	model := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CostPer1MIn: 10, CostPer1MOut: 20}}}
	usage := fantasy.Usage{InputTokens: 1000, OutputTokens: 2000}

	agent.updateSessionUsage(model, currentSession, usage, nil, false)

	require.Equal(t, 1.3, currentSession.Cost)
	require.Equal(t, int64(1000), currentSession.PromptTokens)
	require.Equal(t, int64(2000), currentSession.CompletionTokens)
	require.False(t, currentSession.EstimatedUsage)
}

func TestEstimateFilePartTokens(t *testing.T) {
	t.Parallel()

	// With data present, tokens are estimated from a summary string
	// rather than the raw payload size.
	withData := estimateFilePartTokens(fantasy.FilePart{MediaType: "image/png", Filename: "shot.png", Data: []byte("abc123")})
	require.Equal(t, estimateMediaTokens("image/png", "shot.png", len("abc123")), withData)
	require.Positive(t, withData)

	// With no data, tokens fall back to the media type and filename text.
	noData := estimateFilePartTokens(fantasy.FilePart{MediaType: "image/png", Filename: "shot.png"})
	require.Equal(t, approxTokenCount("image/png")+approxTokenCount("shot.png"), noData)
}

func TestEstimateGeneratedFileTokens(t *testing.T) {
	t.Parallel()

	withData := estimateGeneratedFileTokens(fantasy.FileContent{MediaType: "text/plain", Data: []byte("generated content")})
	require.Equal(t, estimateMediaTokens("text/plain", "", len("generated content")), withData)
	require.Positive(t, withData)

	noData := estimateGeneratedFileTokens(fantasy.FileContent{MediaType: "text/plain"})
	require.Equal(t, approxTokenCount("text/plain"), noData)
	require.Zero(t, estimateGeneratedFileTokens(fantasy.FileContent{}))
}

// TestEstimateStepCompletionTokensSumsEachContentType pins that every
// step content type (and its pointer variant) contributes its own
// estimate, computed by composing the same per-type estimators the
// production switch dispatches to, and that a client-executed tool
// result (ProviderExecuted false) contributes nothing.
func TestEstimateStepCompletionTokensSumsEachContentType(t *testing.T) {
	t.Parallel()

	text := fantasy.TextContent{Text: "hello"}
	textPtr := &fantasy.TextContent{Text: "hello ptr"}
	reasoning := fantasy.ReasoningContent{Text: "reasoning"}
	reasoningPtr := &fantasy.ReasoningContent{Text: "reasoning ptr"}
	file := fantasy.FileContent{MediaType: "text/plain", Data: []byte("data")}
	filePtr := &fantasy.FileContent{MediaType: "text/plain", Data: []byte("data ptr")}
	source := fantasy.SourceContent{SourceType: fantasy.SourceTypeURL, URL: "https://example.com"}
	sourcePtr := &fantasy.SourceContent{SourceType: fantasy.SourceTypeURL, URL: "https://example.com/ptr"}
	toolCall := fantasy.ToolCallContent{ToolName: "bash", Input: `{"a":1}`}
	toolCallPtr := &fantasy.ToolCallContent{ToolName: "bash", Input: `{"a":2}`}
	toolResult := fantasy.ToolResultContent{
		ToolCallID: "1", ToolName: "bash", ProviderExecuted: true,
		Result: fantasy.ToolResultOutputContentText{Text: "value"},
	}
	toolResultPtr := &fantasy.ToolResultContent{
		ToolCallID: "2", ToolName: "bash", ProviderExecuted: true,
		Result: fantasy.ToolResultOutputContentText{Text: "value ptr"},
	}
	clientToolResult := fantasy.ToolResultContent{
		ToolCallID: "3", Result: fantasy.ToolResultOutputContentText{Text: "ignored, not provider-executed"},
	}

	step := fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				text, textPtr, reasoning, reasoningPtr,
				file, filePtr, source, sourcePtr,
				toolCall, toolCallPtr, toolResult, toolResultPtr,
				clientToolResult,
			},
		},
	}

	want := approxTokenCount(text.Text) + approxTokenCount(textPtr.Text) +
		approxTokenCount(reasoning.Text) + approxTokenCount(reasoningPtr.Text) +
		estimateGeneratedFileTokens(file) + estimateGeneratedFileTokens(*filePtr) +
		estimateSourceTokens(source) + estimateSourceTokens(*sourcePtr) +
		estimateToolCallTokens(toolCall.ToolName, toolCall.Input) + estimateToolCallTokens(toolCallPtr.ToolName, toolCallPtr.Input) +
		estimateToolResultContentTokens(toolResult.ToolCallID, toolResult.ToolName, toolResult.ClientMetadata, toolResult.Result) +
		estimateToolResultContentTokens(toolResultPtr.ToolCallID, toolResultPtr.ToolName, toolResultPtr.ClientMetadata, toolResultPtr.Result)

	require.Equal(t, want, estimateStepCompletionTokens(step))
}

// unknownMessagePart implements fantasy.MessagePart without matching any
// of the types estimateMessagePartTokens knows how to size, exercising
// its defensive default branch.
type unknownMessagePart struct{}

func (unknownMessagePart) GetType() fantasy.ContentType     { return "unknown" }
func (unknownMessagePart) Options() fantasy.ProviderOptions { return nil }

// TestEstimateMessagePartTokensCoversAllPartTypes pins that every
// message part type (and its pointer variant) delegates to the same
// per-type estimator exposed for direct use elsewhere.
func TestEstimateMessagePartTokensCoversAllPartTypes(t *testing.T) {
	t.Parallel()

	textPart := fantasy.TextPart{Text: "hello"}
	require.Equal(t, approxTokenCount(textPart.Text), estimateMessagePartTokens(textPart))
	require.Equal(t, approxTokenCount(textPart.Text), estimateMessagePartTokens(&textPart))

	reasoningPart := fantasy.ReasoningPart{Text: "reasoning"}
	require.Equal(t, approxTokenCount(reasoningPart.Text), estimateMessagePartTokens(reasoningPart))
	require.Equal(t, approxTokenCount(reasoningPart.Text), estimateMessagePartTokens(&reasoningPart))

	filePart := fantasy.FilePart{MediaType: "image/png", Filename: "shot.png", Data: []byte("data")}
	require.Equal(t, estimateFilePartTokens(filePart), estimateMessagePartTokens(filePart))
	require.Equal(t, estimateFilePartTokens(filePart), estimateMessagePartTokens(&filePart))

	toolCallPart := fantasy.ToolCallPart{ToolName: "bash", Input: `{"a":1}`}
	require.Equal(t, estimateToolCallTokens(toolCallPart.ToolName, toolCallPart.Input), estimateMessagePartTokens(toolCallPart))
	require.Equal(t, estimateToolCallTokens(toolCallPart.ToolName, toolCallPart.Input), estimateMessagePartTokens(&toolCallPart))

	toolResultPart := fantasy.ToolResultPart{ToolCallID: "1", Output: fantasy.ToolResultOutputContentText{Text: "value"}}
	require.Equal(t, estimateToolResultContentTokens(toolResultPart.ToolCallID, "", "", toolResultPart.Output),
		estimateMessagePartTokens(toolResultPart))
	require.Equal(t, estimateToolResultContentTokens(toolResultPart.ToolCallID, "", "", toolResultPart.Output),
		estimateMessagePartTokens(&toolResultPart))

	require.Zero(t, estimateMessagePartTokens(unknownMessagePart{}), "an unrecognized part type must not panic or guess")
}

// TestEstimateToolResultContentTokensCoversAllOutputTypes pins the
// three output kinds (text, error, media), their pointer variants, and
// the nil-error edge case that must not panic while calling Error().
func TestEstimateToolResultContentTokensCoversAllOutputTypes(t *testing.T) {
	t.Parallel()

	base := approxTokenCount("id") + approxTokenCount("name") + approxTokenCount("meta")

	textOut := fantasy.ToolResultOutputContentText{Text: "value"}
	wantText := base + approxTokenCount("value")
	require.Equal(t, wantText, estimateToolResultContentTokens("id", "name", "meta", textOut))
	require.Equal(t, wantText, estimateToolResultContentTokens("id", "name", "meta", &textOut))

	errOut := fantasy.ToolResultOutputContentError{Error: errors.New("boom")}
	wantErr := base + approxTokenCount("boom")
	require.Equal(t, wantErr, estimateToolResultContentTokens("id", "name", "meta", errOut))
	require.Equal(t, wantErr, estimateToolResultContentTokens("id", "name", "meta", &errOut))

	nilErrOut := fantasy.ToolResultOutputContentError{}
	require.Equal(t, base, estimateToolResultContentTokens("id", "name", "meta", nilErrOut),
		"a nil error must not be dereferenced")

	mediaOut := fantasy.ToolResultOutputContentMedia{MediaType: "image/png", Text: "shot", Data: "bytes"}
	wantMedia := base + estimateMediaTokens("image/png", "shot", len("bytes"))
	require.Equal(t, wantMedia, estimateToolResultContentTokens("id", "name", "meta", mediaOut))
	require.Equal(t, wantMedia, estimateToolResultContentTokens("id", "name", "meta", &mediaOut))
}

func TestEstimateSourceTokens(t *testing.T) {
	t.Parallel()

	source := fantasy.SourceContent{
		SourceType: fantasy.SourceTypeURL,
		ID:         "src-1",
		URL:        "https://example.com",
		Title:      "Example",
		MediaType:  "text/html",
		Filename:   "index.html",
	}
	want := approxTokenCount(string(fantasy.SourceTypeURL)) +
		approxTokenCount("src-1") +
		approxTokenCount("https://example.com") +
		approxTokenCount("Example") +
		approxTokenCount("text/html") +
		approxTokenCount("index.html")
	require.Equal(t, want, estimateSourceTokens(source))
	require.Zero(t, estimateSourceTokens(fantasy.SourceContent{}))
}
