package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	agenttools "github.com/theimaginaryfoundation/what-iff/internal/agent/tools"
	"github.com/theimaginaryfoundation/what-iff/internal/metering"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"go.uber.org/zap"
)

func advancedTestChatCtx() *chatContext {
	c := &chatContext{turnSettings: models.TurnSettings{AgentLoop: models.AgentLoopAdvanced}}
	c.setOfferedTools(map[string]struct{}{agenttools.UpdatePlanToolSpec.Name: {}})
	return c
}

func planUse(t *testing.T, id string, items ...models.PlanItem) provider.ToolUse {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"items": items})
	require.NoError(t, err)
	return provider.ToolUse{ID: id, Name: agenttools.UpdatePlanToolSpec.Name, Input: raw}
}

func unknownUse(id string) provider.ToolUse {
	return provider.ToolUse{ID: id, Name: "unknown_tool", Input: json.RawMessage(`{}`)}
}

func testAdvancedHooks(tr *advancedTranscript) advancedLoopHooks {
	return advancedLoopHooks{
		transcript: tr,
		configure: func(ad provider.AgentAdapter) {
			ad.SetTextDeltaHandler(tr.textDelta)
		},
		config: advancedLoopConfig{maxSteps: 10, maxDuration: time.Minute},
	}
}

func TestAdvancedLoop_SimpleAnswerFinishesInOneStep(t *testing.T) {
	adapter := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeScripted, Script: []provider.MockScriptStep{
		{Text: "Just answering."},
	}})
	chatCtx := advancedTestChatCtx()
	tr := newAdvancedTranscript(nil, nil)
	a := &Agent{logger: zap.NewNop()}

	result, toolCalls, _, err := a.handleAdvancedLoop(context.Background(), chatCtx, adapter, testAdvancedHooks(tr))
	require.NoError(t, err)
	assert.Equal(t, "Just answering.", result.Text)
	assert.Empty(t, result.Reasoning, "the final reply is the message, not reasoning")
	assert.Empty(t, toolCalls)
	assert.Empty(t, adapter.Continuations())
	assert.Len(t, chatCtx.callInputTokens, 1)
}

func TestAdvancedLoop_ContinuesWhilePlanHasOpenItems(t *testing.T) {
	adapter := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeScripted, Script: []provider.MockScriptStep{
		{Text: "Let me plan.", ToolUses: []provider.ToolUse{planUse(t, "p1",
			models.PlanItem{Content: "research", Status: models.PlanItemInProgress},
			models.PlanItem{Content: "write", Status: models.PlanItemPending})}},
		{Text: "Research done, writing next."}, // step 1 ends with open items
		{Text: "Wrapping up.", ToolUses: []provider.ToolUse{planUse(t, "p2",
			models.PlanItem{Content: "research", Status: models.PlanItemCompleted},
			models.PlanItem{Content: "write", Status: models.PlanItemCompleted})}},
		{Text: "Here is the final report."},
	}})
	chatCtx := advancedTestChatCtx()
	tr := newAdvancedTranscript(nil, nil)
	a := &Agent{logger: zap.NewNop()}

	result, toolCalls, _, err := a.handleAdvancedLoop(context.Background(), chatCtx, adapter, testAdvancedHooks(tr))
	require.NoError(t, err)
	assert.Equal(t, "Here is the final report.", result.Text)
	require.Len(t, adapter.Continuations(), 1)
	assert.Contains(t, adapter.Continuations()[0], "[pending] write")
	require.Len(t, toolCalls, 2)
	assert.Equal(t, 1, toolCalls[0].Step)
	assert.Equal(t, 2, toolCalls[1].Step)
	assert.Contains(t, result.Reasoning, "Let me plan.")
	assert.Contains(t, result.Reasoning, "Research done, writing next.")
	assert.Contains(t, result.Reasoning, "— Step 2 —\n\nWrapping up.")
	assert.NotContains(t, result.Reasoning, "final report", "the final reply is not repeated in the reasoning")
	assert.Len(t, chatCtx.callInputTokens, 4, "every model call is reported for metering")
}

func TestAdvancedLoop_InnerCapEndsStepWithoutForcingFinal(t *testing.T) {
	script := make([]provider.MockScriptStep, 0, maxToolCallRounds+2)
	for i := 0; i < maxToolCallRounds+1; i++ {
		script = append(script, provider.MockScriptStep{ToolUses: []provider.ToolUse{unknownUse(fmt.Sprintf("u%d", i))}})
	}
	script = append(script, provider.MockScriptStep{Text: "done after many tools"})
	adapter := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeScripted, Script: script})
	chatCtx := advancedTestChatCtx()
	a := &Agent{logger: zap.NewNop()}

	result, toolCalls, _, err := a.handleAdvancedLoop(context.Background(), chatCtx, adapter, testAdvancedHooks(newAdvancedTranscript(nil, nil)))
	require.NoError(t, err)
	assert.Equal(t, "done after many tools", result.Text)
	assert.Len(t, toolCalls, maxToolCallRounds+1)
	assert.Equal(t, 1, toolCalls[maxToolCallRounds-1].Step)
	assert.Equal(t, 2, toolCalls[maxToolCallRounds].Step, "the round counter resets for the next step")
	assert.Empty(t, adapter.Continuations(), "pending tool results continue the conversation on their own")
}

func TestAdvancedLoop_StallGuardEndsRun(t *testing.T) {
	adapter := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeScripted, Script: []provider.MockScriptStep{
		{ToolUses: []provider.ToolUse{planUse(t, "p1", models.PlanItem{Content: "a", Status: models.PlanItemPending})}},
		{Text: "first"},
		{Text: "second"},
		{Text: "third"},
		{Text: "never reached"},
	}})
	a := &Agent{logger: zap.NewNop()}

	result, _, _, err := a.handleAdvancedLoop(context.Background(), advancedTestChatCtx(), adapter, testAdvancedHooks(newAdvancedTranscript(nil, nil)))
	require.NoError(t, err)
	assert.Equal(t, "third", result.Text)
	assert.Len(t, adapter.Continuations(), 2)
}

func TestAdvancedLoop_StepLimitReturnsLatestReply(t *testing.T) {
	var script []provider.MockScriptStep
	script = append(script, provider.MockScriptStep{ToolUses: []provider.ToolUse{planUse(t, "p", models.PlanItem{Content: "a", Status: models.PlanItemPending})}})
	for i := 0; i < 6; i++ {
		script = append(script,
			provider.MockScriptStep{Text: fmt.Sprintf("reply %d", i)},
			provider.MockScriptStep{ToolUses: []provider.ToolUse{unknownUse(fmt.Sprintf("u%d", i))}})
	}
	adapter := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeScripted, Script: script})
	hooks := testAdvancedHooks(newAdvancedTranscript(nil, nil))
	hooks.config.maxSteps = 3
	a := &Agent{logger: zap.NewNop()}

	result, _, _, err := a.handleAdvancedLoop(context.Background(), advancedTestChatCtx(), adapter, hooks)
	require.NoError(t, err)
	assert.Equal(t, "reply 2", result.Text)
}

func TestAdvancedLoop_QuotaDenialEndsRun(t *testing.T) {
	adapter := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeScripted, Script: []provider.MockScriptStep{
		{ToolUses: []provider.ToolUse{planUse(t, "p", models.PlanItem{Content: "a", Status: models.PlanItemPending})}},
		{Text: "partial answer"},
		{Text: "never reached"},
	}})
	hooks := testAdvancedHooks(newAdvancedTranscript(nil, nil))
	hooks.quotaAllowed = func(context.Context) bool { return false }
	a := &Agent{logger: zap.NewNop()}

	result, _, _, err := a.handleAdvancedLoop(context.Background(), advancedTestChatCtx(), adapter, hooks)
	require.NoError(t, err)
	assert.Equal(t, "partial answer", result.Text)
	assert.Empty(t, adapter.Continuations())
}

func TestAdvancedLoop_CompactsIntoFreshAdapter(t *testing.T) {
	first := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeScripted, EchoText: strings.Repeat("x", 400), Script: []provider.MockScriptStep{
		{ToolUses: []provider.ToolUse{planUse(t, "p", models.PlanItem{Content: "look things up", Status: models.PlanItemInProgress})}},
		{Text: "found part of it"},
	}})
	second := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeScripted, Script: []provider.MockScriptStep{
		{ToolUses: []provider.ToolUse{planUse(t, "p2", models.PlanItem{Content: "look things up", Status: models.PlanItemCompleted})}},
		{Text: "all done"},
	}})
	var rebuiltWith *provider.ModelContext
	tr := newAdvancedTranscript(nil, nil)
	hooks := testAdvancedHooks(tr)
	hooks.config.compactTokens = 10
	hooks.modelContext = &provider.ModelContext{}
	hooks.modelContext.AppendUserMessage(provider.RoleUser, "do the research", nil, false)
	hooks.rebuild = func(mc *provider.ModelContext) (provider.AgentAdapter, error) {
		rebuiltWith = mc
		return second, nil
	}
	chatCtx := advancedTestChatCtx()
	a := &Agent{logger: zap.NewNop()}

	result, _, _, err := a.handleAdvancedLoop(context.Background(), chatCtx, first, hooks)
	require.NoError(t, err)
	assert.Equal(t, "all done", result.Text)
	require.NotNil(t, rebuiltWith)
	var workLog string
	for _, seg := range rebuiltWith.Segments {
		if seg.Kind == provider.SegmentKindDeveloperContext {
			workLog = seg.Content
		}
	}
	assert.Contains(t, workLog, "[in_progress] look things up")
	assert.Contains(t, workLog, "found part of it")
	assert.Contains(t, workLog, "update_plan(")
	assert.Equal(t, provider.SegmentKindUserMessage, rebuiltWith.Segments[len(rebuiltWith.Segments)-1].Kind)
	assert.Empty(t, first.Continuations(), "a compacted run starts fresh instead of continuing the old adapter")
	assert.Len(t, chatCtx.callInputTokens, 4, "calls from the replaced adapter still count")
}

func TestAdvancedTranscript_DiscardDropsOnlyCurrentCallReasoning(t *testing.T) {
	var live strings.Builder
	resets := 0
	tr := newAdvancedTranscript(func(d string) { live.WriteString(d) }, func() { resets++; live.Reset() })
	tr.beginCall()
	tr.nativeDelta("thinking one")
	tr.textDelta("note one")
	tr.beginCall()
	tr.nativeDelta("doomed attempt")
	tr.discardCallNative()
	tr.nativeDelta("thinking two")
	tr.textDelta("final reply")

	assert.Equal(t, 1, resets)
	assert.Equal(t, "thinking one\n\nnote one\n\nthinking two\n\nfinal reply", live.String())
	assert.Equal(t, "thinking one\n\nnote one\n\nthinking two", tr.reasoning(2))
}

func TestWithTurnSettingsUsage(t *testing.T) {
	chatCtx := &chatContext{
		turnSettings:    models.TurnSettings{ReasoningEffort: models.ReasoningEffortHigh, ReasoningEffortSource: models.SettingSourceThread, AgentLoop: models.AgentLoopAdvanced, AgentLoopSource: models.SettingSourceMode},
		reasoningEffort: models.ReasoningEffortHigh,
		callInputTokens: []int64{10, 20},
	}
	u := withTurnSettingsUsage(chatCtx, metering.Usage{})
	assert.Equal(t, "high", u.ReasoningEffort)
	assert.Equal(t, "advanced", u.AgentLoop)
	assert.Equal(t, []int64{10, 20}, u.CallInputTokens)

	// No selection: GLM's always-on default reports no effort, so it is priced as before.
	plain := withTurnSettingsUsage(&chatContext{reasoningEffort: ""}, metering.Usage{})
	assert.Empty(t, plain.ReasoningEffort)
	assert.Equal(t, "standard", plain.AgentLoop)
	assert.Empty(t, plain.CallInputTokens)
}

// Pins the Advanced path's wiring through runGeneration: it runs the Advanced loop (the mock
// implements provider.Continuer) and still reaches the shared empty-turn guard in
// finishGeneration before anything is saved, so it needs no datastore.
func TestRunGeneration_AdvancedTurnUsesSharedGuard(t *testing.T) {
	a := &Agent{logger: zap.NewNop()}
	chatCtx := advancedTestChatCtx()
	chatCtx.chat = &models.Chat{ID: uuid.New(), UserID: uuid.New()}
	chatCtx.model = "mock-model"
	adapter := provider.NewMockAdapter(provider.MockAdapterConfig{Mode: provider.MockModeFixed, FixedResponses: []string{""}})

	agentMessage, result, err := a.runGeneration(context.Background(), chatCtx.chat.UserID, nil,
		&models.ChatMessage{ID: uuid.New(), ChatID: chatCtx.chat.ID}, chatCtx, adapter, generationOptions{provider: "mock"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty response")
	assert.Nil(t, agentMessage)
	assert.Nil(t, result)
	assert.Len(t, chatCtx.callInputTokens, 1, "the Advanced loop ran and recorded its call")
}
