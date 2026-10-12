package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	agenttools "github.com/theimaginaryfoundation/what-iff/internal/agent/tools"
)

// The regression these guard (from the first MCP client): a tool loaded with load_tools must be
// callable by the model in the SAME turn, on the very next agent-loop round, not only on the next
// user turn. Each test drives handleAgentLoop through a real provider adapter against a fake
// model API, so the assertions are on the request bodies the model would actually receive.

// fakeModelAPI answers each request with the next scripted body and records the tool names
// every request offered.
type fakeModelAPI struct {
	mu        sync.Mutex
	responses []func(toolNames []string) string
	offered   [][]string
}

func (f *fakeModelAPI) server(t *testing.T, toolNames func(body map[string]any) []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		names := toolNames(body)
		f.mu.Lock()
		round := len(f.offered)
		f.offered = append(f.offered, names)
		f.mu.Unlock()
		require.Less(t, round, len(f.responses), "unexpected extra model call")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f.responses[round](names))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func toolNamesFromBody(body map[string]any, nameOf func(tool map[string]any) string) []string {
	tools, _ := body["tools"].([]any)
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if m, ok := t.(map[string]any); ok {
			if n := nameOf(m); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// sameTurnFixture is a chat with the test MCP connector attached and web_search loadable, its
// turn tools registered the way a real turn registers them.
func sameTurnFixture(t *testing.T) (*Agent, *chatContext, string, []agenttools.FunctionToolSpec) {
	t.Helper()
	ctx := context.Background()
	mcpSrv := newTestMCPRPCServer()
	t.Cleanup(mcpSrv.Close)
	a, chatCtx, serverID, cleanup := newMCPLifecycleAgentFixture(t, mcpSrv.URL)
	t.Cleanup(cleanup)
	policy := turnToolPolicy{toolsEnabled: true, disabledTools: map[string]bool{}}
	specs := a.turnFunctionToolSpecs(ctx, chatCtx, chatCtx.userID, chatCtx.chat.ID, policy)
	mcpSearch := ""
	for _, s := range specs {
		if strings.HasPrefix(s.Name, "mcp__") && strings.HasSuffix(s.Name, "__search") {
			mcpSearch = s.Name
		}
	}
	require.NotEmpty(t, mcpSearch, "the connector's tools are registered with the adapter")
	require.Contains(t, chatCtx.loadableTools, agenttools.WebSearchFunctionToolSpec.Name)
	return a, chatCtx, serverID.String(), specs
}

func requireSameTurnLoad(t *testing.T, offered [][]string, mcpSearch string, toolErrors []string) {
	t.Helper()
	require.Len(t, offered, 3, "load, use and answer all happen in one turn")
	// Round 1: nothing loaded yet, so neither loadable tool's definition is sent.
	require.Contains(t, offered[0], agenttools.LoadToolsToolSpec.Name)
	require.NotContains(t, offered[0], mcpSearch)
	require.NotContains(t, offered[0], agenttools.WebSearchFunctionToolSpec.Name)
	// Round 2, same turn: the tools just loaded are sent and the call goes through.
	require.Contains(t, offered[1], mcpSearch)
	require.Contains(t, offered[1], agenttools.WebSearchFunctionToolSpec.Name)
	require.Empty(t, toolErrors[0], "load_tools succeeded")
	require.Empty(t, toolErrors[1], "the freshly loaded MCP tool ran in the same turn")
	require.Contains(t, offered[2], mcpSearch, "it stays loaded")
}

func TestToolLoadedMidTurnIsCallableNextRound_Claude(t *testing.T) {
	a, chatCtx, serverID, specs := sameTurnFixture(t)
	mcpSearch := ""
	for _, s := range specs {
		if strings.HasSuffix(s.Name, "__search") {
			mcpSearch = s.Name
		}
	}
	loadInput, _ := json.Marshal(map[string]any{"mcp_server_id": serverID, "tools": []string{"search"}})
	claudeMsg := func(content string, stop string) string {
		return `{"id":"msg","type":"message","role":"assistant","model":"claude-test","content":` + content +
			`,"stop_reason":"` + stop + `","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`
	}
	api := &fakeModelAPI{responses: []func([]string) string{
		func([]string) string {
			return claudeMsg(`[{"type":"tool_use","id":"tu_1","name":"load_tools","input":`+string(loadInput)+`},`+
				`{"type":"tool_use","id":"tu_1b","name":"load_tools","input":{"tools":["web_search"]}}]`, "tool_use")
		},
		func([]string) string {
			return claudeMsg(`[{"type":"tool_use","id":"tu_2","name":"`+mcpSearch+`","input":{"query":"x"}}]`, "tool_use")
		},
		func([]string) string { return claudeMsg(`[{"type":"text","text":"done"}]`, "end_turn") },
	}}
	srv := api.server(t, func(body map[string]any) []string {
		return toolNamesFromBody(body, func(m map[string]any) string { s, _ := m["name"].(string); return s })
	})

	adapter := provider.NewClaudeAdapter(provider.NewClaudeProviderWithBaseURL("test", srv.URL, nil, nil),
		anthropic.MessageNewParams{Model: "claude-test", MaxTokens: 100, Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("search the tracker"))}},
		claudeFunctionTools(specs), false, nil, nil)
	result, toolCalls, _, err := a.handleAgentLoop(context.Background(), chatCtx, adapter)
	require.NoError(t, err)
	require.Equal(t, "done", result.Text)
	require.Len(t, toolCalls, 3)
	requireSameTurnLoad(t, api.offered, mcpSearch, []string{toolCalls[0].ToolError + toolCalls[1].ToolError, toolCalls[2].ToolError})
}

func TestToolLoadedMidTurnIsCallableNextRound_ChatCompletions(t *testing.T) {
	a, chatCtx, serverID, specs := sameTurnFixture(t)
	mcpSearch := ""
	for _, s := range specs {
		if strings.HasSuffix(s.Name, "__search") {
			mcpSearch = s.Name
		}
	}
	loadArgs, _ := json.Marshal(map[string]any{"mcp_server_id": serverID, "tools": []string{"search"}})
	call := func(id, name, args string) string {
		argsJSON, _ := json.Marshal(args)
		return `{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + string(argsJSON) + `}}`
	}
	toolCallMsg := func(calls ...string) string {
		return `{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[` +
			strings.Join(calls, ",") + `]}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	}
	api := &fakeModelAPI{responses: []func([]string) string{
		func([]string) string {
			return toolCallMsg(call("c1", "load_tools", string(loadArgs)), call("c1b", "load_tools", `{"tools":["web_search"]}`))
		},
		func([]string) string { return toolCallMsg(call("c2", mcpSearch, `{"query":"x"}`)) },
		func([]string) string {
			return `{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"done"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		},
	}}
	srv := api.server(t, func(body map[string]any) []string {
		return toolNamesFromBody(body, func(m map[string]any) string {
			fn, _ := m["function"].(map[string]any)
			s, _ := fn["name"].(string)
			return s
		})
	})

	adapter := provider.NewLocalAdapter(provider.NewLocalProvider(srv.URL, nil, nil),
		openai.ChatCompletionNewParams{Model: "m", Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("search the tracker")}},
		openAIChatCompletionFunctionTools(specs), nil)
	result, toolCalls, _, err := a.handleAgentLoop(context.Background(), chatCtx, adapter)
	require.NoError(t, err)
	require.Equal(t, "done", result.Text)
	require.Len(t, toolCalls, 3)
	requireSameTurnLoad(t, api.offered, mcpSearch, []string{toolCalls[0].ToolError + toolCalls[1].ToolError, toolCalls[2].ToolError})
}
