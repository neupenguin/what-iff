package models

import "strings"

// Model-name rules for reasoning effort (ADR 0x026), in one place. Providers name their model
// families by prefix, and which reasoning controls a model takes depends on the family, not only
// the provider. ReasoningSupportFor (reasoning.go) and the provider request mappings
// (internal/agent/provider/reasoning_effort.go) both read these tables, so a new model family is
// a table edit here. Each table cites the provider doc it follows; models.reasoning_test.go pins
// the matrix.

// --- Anthropic (platform.claude.com/docs/en/build-with-claude/extended-thinking, /effort) ---

// anthropicAlwaysThinking are models that reject a request disabling thinking: effort is their
// only control. Sonnet 5.5 can only switch thinking off with a "between_tools" mode the request
// layer does not model; treating it as always-on keeps off at low effort, which it accepts.
var anthropicAlwaysThinking = []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable", "claude-mythos"}

// anthropicSummarizesByDefault are models that return summarized thinking without a display
// setting (the 4.6 generation), so none is sent.
var anthropicSummarizesByDefault = []string{"claude-opus-4-6", "claude-sonnet-4-6"}

// anthropicThinkingBudget are models that predate adaptive thinking and effort: they take a
// thinking budget instead.
var anthropicThinkingBudget = []string{"claude-haiku-4-5"}

// IsAnthropicAlwaysThinkingModel reports whether a native Anthropic model cannot disable thinking.
func IsAnthropicAlwaysThinkingModel(name string) bool {
	return hasPrefix(name, anthropicAlwaysThinking)
}

// AnthropicSummarizesThinkingByDefault reports whether a model needs no thinking display setting.
func AnthropicSummarizesThinkingByDefault(name string) bool {
	return hasPrefix(name, anthropicSummarizesByDefault)
}

// AnthropicUsesThinkingBudget reports whether a model takes budget_tokens rather than effort.
func AnthropicUsesThinkingBudget(name string) bool { return hasPrefix(name, anthropicThinkingBudget) }

// --- OpenAI (developers.openai.com/api/docs/guides/reasoning) ---

// openAIReasoningPrefixes are the reasoning model families.
var openAIReasoningPrefixes = []string{"gpt-5", "o1", "o3", "o4"}

// openAIMinimalOnly are the original GPT-5 models, which take "minimal" but not "none" (gpt-5.1
// and later take "none"). Dated snapshots of these names are covered by OpenAIRejectsNoneEffort.
var openAIMinimalOnly = []string{"gpt-5", "gpt-5-mini", "gpt-5-nano"}

// OpenAIRejectsNoneEffort reports whether a model's lowest effort is "minimal" rather than "none".
func OpenAIRejectsNoneEffort(name string) bool {
	n := normalizeModelName(name)
	for _, m := range openAIMinimalOnly {
		if n == m || strings.HasPrefix(n, m+"-20") {
			return true
		}
	}
	return false
}

// --- Gemini (ai.google.dev/gemini-api/docs/openai, /thinking) ---

// geminiNoReasoning marks media and non-chat model names; geminiLegacy are families without
// thinking control; geminiThinkingOptional can disable thinking (budget 0 / effort "none").
// Every other Gemini model (2.5 Pro, 3.x, 4.x) always thinks.
var (
	geminiNoReasoning      = []string{"image", "tts", "live", "embedding", "nano-banana", "veo", "imagen"}
	geminiLegacy           = []string{"gemini-1", "gemini-2.0"}
	geminiThinkingOptional = []string{"gemini-2.5-flash"}
)

func geminiReasoningSupport(n string) ReasoningSupport {
	switch {
	case hasAny(n, geminiNoReasoning...), hasPrefix(n, geminiLegacy):
		return ReasoningSupportNone
	case hasPrefix(n, geminiThinkingOptional):
		return ReasoningSupportOptional
	}
	return ReasoningSupportAlways
}

// --- Qwen (alibabacloud.com/help/en/model-studio/deep-thinking) ---

var (
	// qwenThinkingOnly cannot disable thinking.
	qwenThinkingOnly = []string{"qwen3.8-2.4t", "qwen3.7-max-preview", "qwen3.7-max-2026-05-17"}
	// qwenNoReasoning marks media, translation and embedding names.
	qwenNoReasoning = []string{"image", "tts", "embedding", "-mt-", "-vl", "asr"}
	// qwenHybrid take enable_thinking (Qwen 3.x and the commercial qwen-plus / qwen-flash).
	qwenHybrid = []string{"qwen3", "qwen-plus", "qwen-flash"}
	// qwenEffort take reasoning_effort (low/medium/xhigh) instead of a thinking budget.
	qwenEffort = []string{"qwen3.8"}
)

func qwenReasoningSupport(n string) ReasoningSupport {
	switch {
	case hasPrefix(n, qwenThinkingOnly):
		return ReasoningSupportAlways
	case hasAny(n, qwenNoReasoning...):
		return ReasoningSupportNone
	case hasPrefix(n, qwenHybrid):
		return ReasoningSupportOptional
	}
	return ReasoningSupportNone
}

// QwenUsesReasoningEffort reports whether a Qwen model takes reasoning_effort rather than a
// thinking budget (DashScope rejects the two together).
func QwenUsesReasoningEffort(name string) bool { return hasPrefix(name, qwenEffort) }

// --- DeepSeek (api-docs.deepseek.com/guides/thinking_mode) ---

// deepSeekNoReasoning marks non-chat names; every other DeepSeek model is a hybrid
// (deepseek-flash, deepseek-v4-pro): reasoning_effort none/low/high/max, thinking on by default.
var deepSeekNoReasoning = []string{"-vl", "ocr", "coder"}

func deepSeekReasoningSupport(n string) ReasoningSupport {
	if hasAny(n, deepSeekNoReasoning...) {
		return ReasoningSupportNone
	}
	return ReasoningSupportOptional
}

// --- Mistral (docs.mistral.ai/studio/conversations/reasoning) ---

// mistralReasoning are the hybrid models documented to take reasoning_effort ("none"/"high" only,
// hence a toggle). Aliases whose target is unconfirmed (mistral-large-latest,
// mistral-medium-latest) are left out until verified.
var mistralReasoning = []string{"mistral-small-latest", "mistral-small-4", "mistral-medium-3-5", "mistral-medium-3.5", "mistral-large-4"}

func mistralReasoningSupport(n string) ReasoningSupport {
	if hasPrefix(n, mistralReasoning) {
		return ReasoningSupportToggle
	}
	return ReasoningSupportNone
}

// --- helpers ---

func normalizeModelName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func hasPrefix(name string, prefixes []string) bool {
	n := normalizeModelName(name)
	for _, p := range prefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
