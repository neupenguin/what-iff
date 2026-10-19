package agent

import (
	"context"

	"github.com/theimaginaryfoundation/what-iff/internal/metering"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"go.uber.org/zap"
)

// resolveTurnSettings fixes the turn's reasoning effort and agent loop (ADR 0x026): an agent
// job's values (on ctx) win, then the active mode's, then the thread's own (seeded from the
// personality or account default when the thread was created). mode is the turn's active mode
// (nil for none). Call it once the model and the mode are final for the turn.
func (a *Agent) resolveTurnSettings(ctx context.Context, chatCtx *chatContext, mode *models.Mood) {
	if chatCtx == nil {
		return
	}
	var threadLevel, modeLevel models.TurnSettingLevel
	if chatCtx.chat != nil {
		threadLevel = chatCtx.chat.TurnSettingValues.Level()
	}
	if mode != nil {
		modeLevel = mode.TurnSettingValues.Level()
	}
	settings := models.ResolveTurnSettings(threadLevel, modeLevel, models.JobTurnSettingsFromContext(ctx))
	if settings.Advanced() && a.localLLM {
		// The local backend keeps its own generation pipeline, which has no Advanced loop.
		a.logger.Warn("Advanced agent loop selected but LLM_BACKEND=local has no Advanced loop; running Standard",
			zap.String("agent_loop_source", string(settings.AgentLoopSource)))
		settings.AgentLoop = models.AgentLoopStandard
	}
	chatCtx.turnSettings = settings
	chatCtx.reasoningEffort = models.EffectiveReasoningEffort(models.ReasoningSupportFor(chatCtx.modelProvider, chatCtx.model), settings.ReasoningEffort)
	if settings.ReasoningEffort != "" || settings.AgentLoop != "" {
		a.logger.Info("turn settings resolved",
			zap.String("model", chatCtx.model),
			zap.String("reasoning_effort", string(settings.ReasoningEffort)),
			zap.String("reasoning_effort_source", string(settings.ReasoningEffortSource)),
			zap.String("effective_reasoning_effort", string(chatCtx.reasoningEffort)),
			zap.String("agent_loop", string(settings.AgentLoop)),
			zap.String("agent_loop_source", string(settings.AgentLoopSource)),
		)
	}
}

// threadActiveMood loads the thread's current active mode for runs that do not select one
// (agent jobs, background webhook prompts), so its settings still apply. Nil when the thread
// has no active mode or it cannot be loaded.
func (a *Agent) threadActiveMood(ctx context.Context, chatCtx *chatContext) *models.Mood {
	if chatCtx == nil || chatCtx.chat == nil || chatCtx.chat.ActiveMoodID == nil {
		return nil
	}
	mood, err := a.ds.GetMood(ctx, chatCtx.chat.UserID, *chatCtx.chat.ActiveMoodID)
	if err != nil {
		a.logger.Warn("failed to load the thread's active mode for turn settings", zap.Error(err))
		return nil
	}
	return mood
}

// withTurnSettingsUsage adds the turn's reasoning effort and agent loop to a turn's usage
// (ADR 0x026). The effort is reported only when one was selected and the model honours it, so
// turns without a selection are metered as before; an Advanced turn also reports every call's
// input tokens.
func withTurnSettingsUsage(chatCtx *chatContext, u metering.Usage) metering.Usage {
	if chatCtx == nil {
		return u
	}
	if chatCtx.turnSettings.ReasoningEffortSource != models.SettingSourceUnset {
		u.ReasoningEffort = string(chatCtx.reasoningEffort)
	}
	if chatCtx.turnSettings.Advanced() {
		u.AgentLoop = string(models.AgentLoopAdvanced)
		u.CallInputTokens = append([]int64(nil), chatCtx.callInputTokens...)
	} else {
		u.AgentLoop = string(models.AgentLoopStandard)
	}
	return u
}

// withTurnAgentLoop marks an Advanced turn's context for Meter.Check (metering.WithAgentLoop).
func withTurnAgentLoop(ctx context.Context, chatCtx *chatContext) context.Context {
	if chatCtx != nil && chatCtx.turnSettings.Advanced() {
		return metering.WithAgentLoop(ctx, string(models.AgentLoopAdvanced))
	}
	return ctx
}

type subagentReasoningEffortKey struct{}

// withSubagentReasoningEffort carries the parent turn's selected effort to a sub-agent call.
func withSubagentReasoningEffort(ctx context.Context, effort models.ReasoningEffort) context.Context {
	if effort == "" {
		return ctx
	}
	return context.WithValue(ctx, subagentReasoningEffortKey{}, effort)
}

// subagentReasoningEffort is the effort a sub-agent inherits ("" for none).
func subagentReasoningEffort(ctx context.Context) models.ReasoningEffort {
	v, _ := ctx.Value(subagentReasoningEffortKey{}).(models.ReasoningEffort)
	return v
}
