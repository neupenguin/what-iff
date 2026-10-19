package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	agenttools "github.com/theimaginaryfoundation/what-iff/internal/agent/tools"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"go.uber.org/zap"
)

// The Advanced (ReAct) loop, ADR 0x026: steps of the Standard tool loop, each with its round
// counter reset, driven by the model's plan until no work remains.

const (
	defaultAdvancedMaxSteps      = 50
	defaultAdvancedMaxDuration   = 60 * time.Minute
	defaultAdvancedCompactTokens = 120_000
	// advancedMaxStalls ends a run after this many continuation nudges in a row that produced
	// no tool call and no plan change: the model has stopped working, nudging again won't help.
	advancedMaxStalls = 2
	// advancedWorkLogMaxChars bounds the compacted work log.
	advancedWorkLogMaxChars = 24_000
)

// advancedLoopInstructions are added (as developer context) to every Advanced turn.
const advancedLoopInstructions = `You are working in extended mode: you can take many steps and tool calls to finish this request, so do the work thoroughly rather than stopping early.

For work that takes several steps, keep a checklist with update_plan: write the steps, mark one in_progress while you work on it, and mark each completed or cancelled as you go. You will be asked to continue while any item is pending or in_progress.

Text you write before your final reply is shown to the user as your working notes, not as the answer. When everything is done, write the final reply so it stands on its own (it is the only part kept as your answer) and call no more tools.`

// advancedLoopConfig bounds an Advanced run. Read from the environment on each run so an
// operator can tune it without a restart of the code path (tests set the fields directly).
type advancedLoopConfig struct {
	maxSteps      int
	maxDuration   time.Duration
	compactTokens int64
}

func advancedLoopConfigFromEnv(logger *zap.Logger) advancedLoopConfig {
	return advancedLoopConfigFrom(os.Getenv, logger)
}

// advancedLoopConfigFrom reads the limits through getenv. A set but invalid or non-positive value
// is logged and the default is used: these limits bound a run's cost, so a typo should be visible.
func advancedLoopConfigFrom(getenv func(string) string, logger *zap.Logger) advancedLoopConfig {
	cfg := advancedLoopConfig{
		maxSteps:      defaultAdvancedMaxSteps,
		maxDuration:   defaultAdvancedMaxDuration,
		compactTokens: defaultAdvancedCompactTokens,
	}
	invalid := func(name, raw string) {
		if logger != nil {
			logger.Warn("ignoring invalid Advanced loop limit; using the default", zap.String("env", name), zap.String("value", raw))
		}
	}
	if raw := strings.TrimSpace(getenv("AGENT_ADVANCED_MAX_STEPS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			cfg.maxSteps = v
		} else {
			invalid("AGENT_ADVANCED_MAX_STEPS", raw)
		}
	}
	if raw := strings.TrimSpace(getenv("AGENT_ADVANCED_MAX_DURATION")); raw != "" {
		if v, err := time.ParseDuration(raw); err == nil && v > 0 {
			cfg.maxDuration = v
		} else {
			invalid("AGENT_ADVANCED_MAX_DURATION", raw)
		}
	}
	if raw := strings.TrimSpace(getenv("AGENT_ADVANCED_COMPACT_TOKENS")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			cfg.compactTokens = v
		} else {
			invalid("AGENT_ADVANCED_COMPACT_TOKENS", raw)
		}
	}
	return cfg
}

// advancedLoopHooks are what the loop needs from the generation path that runs it.
type advancedLoopHooks struct {
	transcript *advancedTranscript
	// configure installs the streaming handlers on an adapter (the first one, and any rebuilt
	// by compaction).
	configure func(adapter provider.AgentAdapter)
	// rebuild builds a fresh adapter from a ModelContext (compaction). Nil disables compaction.
	rebuild func(mc *provider.ModelContext) (provider.AgentAdapter, error)
	// modelContext is the turn's original context, which compaction starts again from.
	modelContext *provider.ModelContext
	// quotaAllowed re-checks the account's quota between steps. Nil means always allowed.
	quotaAllowed func(ctx context.Context) bool
	config       advancedLoopConfig
}

// advancedRun is one Advanced run's state. It, and the chatContext it updates (callInputTokens,
// plan, planVersion), are owned by the turn's agent-loop goroutine: nothing else reads or writes
// them while the run is in progress (the live timeline goes through jobToolProgress, which has its
// own lock, and metering reads callInputTokens only after the run returns). No locking is needed
// as long as that holds; code that adds another goroutine must add it.
type advancedRun struct {
	a       *Agent
	chatCtx *chatContext
	hooks   advancedLoopHooks
	adapter provider.AgentAdapter
	// retiredUsage is the per-call usage of adapters replaced by compaction.
	retiredUsage []provider.CallUsage
	toolCalls    []*models.ToolCall
	attachments  []*models.FileAttachment
	// compactedAtCall is the last transcript call a compaction folded into the work log.
	compactedAtCall int
}

// handleAdvancedLoop runs the Advanced loop on adapter, which must implement provider.Continuer.
// It returns like handleAgentLoop; the final response's Reasoning is the run's transcript.
func (a *Agent) handleAdvancedLoop(ctx context.Context, chatCtx *chatContext, adapter provider.AgentAdapter, hooks advancedLoopHooks) (*provider.GenerateResponse, []*models.ToolCall, []*models.FileAttachment, error) {
	run := &advancedRun{a: a, chatCtx: chatCtx, hooks: hooks, adapter: adapter}
	if hooks.configure != nil {
		hooks.configure(adapter)
	}
	result, err := run.loop(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	run.attachments = a.appendPostToolLoopGeneratedAttachments(ctx, chatCtx, run.attachments)
	if hooks.transcript != nil {
		result.Reasoning = hooks.transcript.reasoning(hooks.transcript.currentCall())
	}
	return result, run.toolCalls, run.attachments, nil
}

func (r *advancedRun) loop(ctx context.Context) (*provider.GenerateResponse, error) {
	cfg := r.hooks.config
	start := time.Now()
	stalls := 0
	var last *provider.GenerateResponse
	pendingToolResults := false

	for step := 1; step <= cfg.maxSteps; step++ {
		r.chatCtx.toolProgress.SetStep(step)
		if step > 1 && r.hooks.transcript != nil {
			r.hooks.transcript.stepHeader(step)
		}
		planBefore := r.chatCtx.planVersion
		res, calls, atts, capped, err := r.a.runToolRounds(ctx, r.chatCtx, r.adapter, toolRoundsOptions{
			step:                step,
			endStepAtCap:        true,
			skipPostAttachments: true,
			beforeCall:          r.beginCall,
			afterCall:           r.syncUsage,
		})
		r.syncUsage()
		if err != nil {
			return nil, err
		}
		r.toolCalls = append(r.toolCalls, calls...)
		r.attachments = append(r.attachments, atts...)
		pendingToolResults = capped
		if !capped {
			last = res
			open := openPlanItems(r.chatCtx.plan)
			if len(open) == 0 {
				r.logEnd("done", step)
				return res, nil
			}
			if len(calls) == 0 && r.chatCtx.planVersion == planBefore {
				stalls++
			} else {
				stalls = 0
			}
			if stalls >= advancedMaxStalls {
				r.logEnd("stalled", step)
				return res, nil
			}
		} else {
			stalls = 0
		}

		// Between steps: stop on cancellation, the time limit or the quota; then compact.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Since(start) > cfg.maxDuration {
			r.logEnd("duration_limit", step)
			return r.finish(ctx, last, pendingToolResults)
		}
		if r.hooks.quotaAllowed != nil && !r.hooks.quotaAllowed(ctx) {
			r.logEnd("quota", step)
			return r.finish(ctx, last, pendingToolResults)
		}
		if step == cfg.maxSteps {
			break
		}
		if r.shouldCompact() {
			if err := r.compact(ctx, step); err != nil {
				r.a.logger.Warn("advanced loop compaction failed; continuing uncompacted", zap.Error(err))
			} else {
				// A fresh adapter starts from the original request plus the work log, so there is
				// nothing pending and no continuation to append.
				pendingToolResults = false
				continue
			}
		}
		if !capped {
			r.adapter.(provider.Continuer).ContinueWith(continuationMessage(r.chatCtx.plan))
		}
	}
	r.logEnd("step_limit", cfg.maxSteps)
	return r.finish(ctx, last, pendingToolResults)
}

// finish ends a run that stopped before the model finished: the latest reply stands when the
// model had just given one; otherwise (tool results pending) one last answer is forced.
func (r *advancedRun) finish(ctx context.Context, last *provider.GenerateResponse, pendingToolResults bool) (*provider.GenerateResponse, error) {
	if !pendingToolResults && last != nil {
		return last, nil
	}
	r.beginCall()
	final, err := r.adapter.ForceFinalResponse(ctx)
	r.syncUsage()
	if err != nil {
		return nil, fmt.Errorf("failed to force final response: %w", err)
	}
	return final, nil
}

func (r *advancedRun) beginCall() {
	if r.hooks.transcript != nil {
		r.hooks.transcript.beginCall()
	}
}

// syncUsage refreshes the turn's per-call input tokens (metering) from every adapter so far.
func (r *advancedRun) syncUsage() {
	usage := r.callUsage()
	tokens := make([]int64, len(usage))
	for i, u := range usage {
		tokens[i] = u.InputTokens
	}
	r.chatCtx.callInputTokens = tokens
}

func (r *advancedRun) callUsage() []provider.CallUsage {
	out := append([]provider.CallUsage(nil), r.retiredUsage...)
	if rep, ok := r.adapter.(provider.UsageReporter); ok {
		out = append(out, rep.CallUsage()...)
	}
	return out
}

// shouldCompact reports whether the last call's input passed the compaction threshold.
func (r *advancedRun) shouldCompact() bool {
	if r.hooks.rebuild == nil || r.hooks.modelContext == nil || r.hooks.config.compactTokens <= 0 {
		return false
	}
	rep, ok := r.adapter.(provider.UsageReporter)
	if !ok {
		return false
	}
	usage := rep.CallUsage()
	return len(usage) > 0 && usage[len(usage)-1].InputTokens > r.hooks.config.compactTokens
}

// compact replaces the adapter with a fresh one built from the original request plus a work log
// of everything done so far.
func (r *advancedRun) compact(ctx context.Context, step int) error {
	mc := r.hooks.modelContext.Clone()
	mc.InsertBeforeLastUserMessage(provider.SegmentKindDeveloperContext, provider.RoleDeveloper, r.workLog(step), false)
	next, err := r.hooks.rebuild(mc)
	if err != nil {
		return err
	}
	if _, ok := next.(provider.Continuer); !ok {
		return fmt.Errorf("rebuilt adapter %T cannot continue", next)
	}
	if rep, ok := r.adapter.(provider.UsageReporter); ok {
		r.retiredUsage = append(r.retiredUsage, rep.CallUsage()...)
	}
	if r.hooks.configure != nil {
		r.hooks.configure(next)
	}
	r.adapter = next
	if r.hooks.transcript != nil {
		r.compactedAtCall = r.hooks.transcript.currentCall()
	}
	r.a.logger.Info("advanced loop compacted its context", zap.Int("step", step), zap.Int("tool_calls", len(r.toolCalls)))
	return nil
}

// workLog is the developer context a compacted run continues from: the plan, the notes the model
// wrote, and its tool calls with truncated inputs and outputs. The header, plan and notes count
// toward advancedWorkLogMaxChars first; tool calls fill what is left, newest first, so the oldest
// calls are the ones dropped. The kept calls are then written oldest first.
func (r *advancedRun) workLog(step int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are partway through the user's latest message below, in extended mode (step %d). Earlier work was compacted to save space; here is what you have done so far. Continue from here without repeating finished work.\n", step)
	if len(r.chatCtx.plan) > 0 {
		fmt.Fprintf(&b, "\nYour plan:\n%s\n", formatPlan(r.chatCtx.plan))
	}
	if r.hooks.transcript != nil {
		if notes := r.hooks.transcript.textSince(0); notes != "" {
			fmt.Fprintf(&b, "\nYour notes so far:\n%s\n", agenttools.TruncateRunes(notes, advancedWorkLogMaxChars/3))
		}
	}
	budget := advancedWorkLogMaxChars - b.Len()
	var lines []string
	for i := len(r.toolCalls) - 1; i >= 0 && budget > 0; i-- {
		tc := r.toolCalls[i]
		result := tc.ToolOutput
		if tc.ToolError != "" {
			result = "error: " + tc.ToolError
		}
		line := fmt.Sprintf("- step %d: %s(%s) → %s", tc.Step, tc.ToolName,
			agenttools.TruncateRunes(tc.ToolInput, 300), agenttools.TruncateRunes(result, 600))
		budget -= len(line)
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		if len(lines) < len(r.toolCalls) {
			fmt.Fprintf(&b, "\nTool calls (newest %d of %d; older ones omitted):\n", len(lines), len(r.toolCalls))
		} else {
			b.WriteString("\nTool calls so far:\n")
		}
		for i := len(lines) - 1; i >= 0; i-- {
			b.WriteString(lines[i])
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (r *advancedRun) logEnd(reason string, step int) {
	r.a.logger.Info("advanced loop finished",
		zap.String("reason", reason),
		zap.Int("steps", step),
		zap.Int("tool_calls", len(r.toolCalls)),
		zap.Int("model_calls", len(r.chatCtx.callInputTokens)),
		zap.Int("open_plan_items", len(openPlanItems(r.chatCtx.plan))),
	)
}

// continuationMessage asks the model to keep going on its open plan items.
func continuationMessage(plan []models.PlanItem) string {
	return "You still have open items in your plan:\n" + formatPlan(openPlanItems(plan)) +
		"\n\nContinue working on them. If an item is no longer needed, mark it cancelled with update_plan. When everything is done, give your final answer."
}

// runAdvancedGeneration is runGeneration for an Advanced turn. Everything the model writes is
// streamed as reasoning (the transcript); the final call's reply is saved as the message.
func (a *Agent) runAdvancedGeneration(ctx context.Context, userID uuid.UUID, chatJob *models.Job, chatMessage *models.ChatMessage, chatCtx *chatContext, adapter provider.AgentAdapter, opts generationOptions) (*models.ChatMessage, *provider.GenerateResponse, error) {
	reasoningBuffer := newJobDraftReasoningBuffer(a.lifecycleCtx, a.ds, a.logger, chatJob, jobDraftDeltaFlushMinChars, jobDraftDeltaFlushMaxWait)
	defer reasoningBuffer.Flush()
	transcript := newAdvancedTranscript(reasoningBuffer.HandleDelta, reasoningBuffer.ResetReasoning)

	current := adapter
	configure := func(ad provider.AgentAdapter) {
		current = ad
		ad.SetTextDeltaHandler(transcript.textDelta)
		if streamer, ok := ad.(provider.ReasoningStreamer); ok {
			streamer.SetReasoningStream(provider.ReasoningStream{
				OnDelta:       transcript.nativeDelta,
				OnDiscardCall: transcript.discardCallNative,
			})
		}
	}
	// Persist buffered notes before each tool row, so they reach the client ahead of it.
	chatCtx.toolProgress = a.newChatToolProgress(chatJob, reasoningBuffer.Flush)
	chatCtx.toolProgress.Seed(chatCtx.memoryProgress.Entries())
	if progress := chatCtx.toolProgress; progress != nil {
		defer progress.Close()
	}

	actionType := models.ActionTypeChatMessage
	if chatJob != nil && chatJob.JobType == JobTypeAgentJobRun {
		actionType = models.ActionTypeJobRun
	}
	hooks := advancedLoopHooks{
		transcript:   transcript,
		configure:    configure,
		rebuild:      opts.rebuild,
		modelContext: opts.modelContext,
		quotaAllowed: func(ctx context.Context) bool {
			return a.meter.Check(ctx, userID, chatCtx.modelSubscriptionTier, actionType).Allowed
		},
		config: advancedLoopConfigFromEnv(a.logger),
	}
	result, toolCalls, generatedAttachments, err := a.handleAdvancedLoop(ctx, chatCtx, adapter, hooks)
	if err != nil {
		return nil, nil, fmt.Errorf("%s advanced loop failed: %w", opts.provider, err)
	}
	if result == nil {
		return nil, nil, fmt.Errorf("%s advanced loop returned nil response with no error", opts.provider)
	}
	result.Text = strings.TrimSpace(result.Text)
	return a.finishGeneration(ctx, userID, chatMessage, chatCtx, current, opts, result, toolCalls, generatedAttachments)
}
