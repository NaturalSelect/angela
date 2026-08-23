package model

import (
	"cmp"
	"fmt"

	"github.com/NaturalSelect/angela/internal/ui/common"
)

// modelInfo renders the current model information including reasoning
// settings and context usage/cost.
func (m *UI) modelInfo(width int) string {
	active := m.activeAgent()
	reasoningInfo := ""
	providerName := ""

	if active != nil {
		// Get provider name first
		providerConfig, ok := m.com.Config().Providers.Get(active.ModelCfg.Provider)
		if ok {
			providerName = providerConfig.Name

			// ModelCfg already has the session's preset folded in, so
			// what is rendered here is what the turn actually runs on.
			if active.CatwalkCfg.CanReason {
				if len(active.CatwalkCfg.ReasoningLevels) == 0 {
					if active.ModelCfg.Think {
						reasoningInfo = "Thinking On"
					} else {
						reasoningInfo = "Thinking Off"
					}
				} else {
					reasoningEffort := cmp.Or(active.ModelCfg.ReasoningEffort, active.CatwalkCfg.DefaultReasoningEffort)
					reasoningInfo = fmt.Sprintf("Reasoning %s", common.FormatReasoningEffort(reasoningEffort))
				}
			}
		}
	}

	var modelContext *common.ModelContextInfo
	if active != nil && m.session != nil {
		modelContext = &common.ModelContextInfo{
			ContextUsed:    m.session.CompletionTokens + m.session.PromptTokens,
			Cost:           m.session.Cost,
			ModelContext:   active.CatwalkCfg.ContextWindow,
			EstimatedUsage: m.session.EstimatedUsage,
		}
	}
	var modelName string
	if active != nil {
		modelName = active.CatwalkCfg.Name
	}
	return common.ModelInfo(m.com.Styles, modelName, providerName, reasoningInfo, modelContext, width)
}
