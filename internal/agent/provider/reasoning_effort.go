package provider

import (
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// User-selected reasoning effort (ADR 0x026) mapped onto each provider's request. Every
// function here takes the effective effort (models.EffectiveReasoningEffort): "" means no
// selection, and the request is left exactly as it was before the setting existed.

// ApplyOpenAIReasoning sets reasoning.effort on a Responses request. off maps to "none", or
// "minimal" for the original GPT-5 models, which do not accept "none". Any other level also
// asks for reasoning summaries, which the adapter captures as the turn's reasoning text, and
// raises the output cap so reasoning does not crowd out the reply.
func ApplyOpenAIReasoning(params *responses.ResponseNewParams, effort models.ReasoningEffort) {
	if params == nil || effort == "" {
		return
	}
	if effort == models.ReasoningEffortOff {
		params.Reasoning.Effort = openAINoReasoningEffort(string(params.Model))
		return
	}
	params.Reasoning.Effort = shared.ReasoningEffort(effort)
	params.Reasoning.Summary = shared.ReasoningSummaryAuto
	raiseOpenAIOutputCap(params)
}

// openAINoReasoningEffort is the lowest effort a GPT-5-family model accepts: "none" from
// gpt-5.1 on, "minimal" before it.
func openAINoReasoningEffort(model string) shared.ReasoningEffort {
	m := strings.ToLower(strings.TrimSpace(model))
	switch m {
	case "gpt-5", "gpt-5-mini", "gpt-5-nano":
		return shared.ReasoningEffortMinimal
	}
	if strings.HasPrefix(m, "gpt-5-") && !strings.HasPrefix(m, "gpt-5-1") {
		// Dated snapshots of the original models (gpt-5-2025-…, gpt-5-mini-2025-…).
		return shared.ReasoningEffortMinimal
	}
	return shared.ReasoningEffortNone
}

func raiseOpenAIOutputCap(params *responses.ResponseNewParams) {
	// Only raise a cap that is set; an uncapped request stays uncapped.
	if params.MaxOutputTokens.Valid() && params.MaxOutputTokens.Value < ReasoningMaxOutputTokens {
		params.MaxOutputTokens = openai.Int(ReasoningMaxOutputTokens)
	}
}

// ApplyClaudeReasoning maps an effort onto a native Anthropic Messages request and reports
// whether thinking blocks must be replayed across tool rounds (the API requires the assistant
// turn that called a tool to keep its thinking when thinking is on).
//   - low/medium/high: adaptive thinking with summarized display and output_config.effort;
//     Haiku 4.5, which predates adaptive thinking and effort, gets a thinking budget instead.
//   - off: thinking disabled. Models that cannot disable thinking never get off: their
//     reasoning support is "always", which runs off as low.
func ApplyClaudeReasoning(params *anthropic.MessageNewParams, effort models.ReasoningEffort) (replayThinking bool) {
	if params == nil || effort == "" {
		return false
	}
	model := strings.ToLower(string(params.Model))
	if effort == models.ReasoningEffortOff {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
		return false
	}
	if params.MaxTokens < ReasoningMaxOutputTokens {
		params.MaxTokens = ReasoningMaxOutputTokens
	}
	if strings.HasPrefix(model, "claude-haiku-4-5") {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(claudeThinkingBudget(effort))
		return true
	}
	adaptive := anthropic.ThinkingConfigAdaptiveParam{}
	if !claudeSummarizesByDefault(model) {
		// Newer models omit thinking text unless asked; the turn shows it, so ask for summaries.
		adaptive.SetExtraFields(map[string]any{"display": "summarized"})
	}
	params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
	params.OutputConfig.Effort = anthropic.OutputConfigEffort(effort)
	return true
}

// claudeSummarizesByDefault reports whether a model returns summarized thinking without a
// display setting (the 4.6 generation), so no display field is sent to it.
func claudeSummarizesByDefault(model string) bool {
	return strings.HasPrefix(model, "claude-opus-4-6") || strings.HasPrefix(model, "claude-sonnet-4-6")
}

// claudeThinkingBudget is the budget_tokens for models that still take one. It stays under
// ReasoningMaxOutputTokens so the reply keeps room.
func claudeThinkingBudget(effort models.ReasoningEffort) int64 {
	switch effort {
	case models.ReasoningEffortLow:
		return 2048
	case models.ReasoningEffortHigh:
		return 12288
	}
	return 6144
}

// ZAIReasoningEffortFor maps an effort onto z.ai GLM's output_config.effort, which accepts only
// low/high/max. GLM always reasons: no selection keeps today's fixed ZAIReasoningEffort, and
// medium maps to that same value.
func ZAIReasoningEffortFor(effort models.ReasoningEffort) anthropic.OutputConfigEffort {
	switch effort {
	case models.ReasoningEffortOff, models.ReasoningEffortLow:
		return anthropic.OutputConfigEffortLow
	case models.ReasoningEffortHigh:
		return anthropic.OutputConfigEffortMax
	}
	return ZAIReasoningEffort
}
