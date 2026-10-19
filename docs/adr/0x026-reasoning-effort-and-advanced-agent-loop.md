# ADR 0x026: Reasoning effort and the Advanced (ReAct) agent loop

- **Status:** Proposed (implementation in progress)
- **Date:** 2026-10-18
- **Deciders:** What Iff maintainers
- **Builds on:** the shared tool loop (`internal/agent/agentloop.go`), the provider `AgentAdapter` seam, live reasoning streaming (`Job.draft_reasoning` → `ChatMessage.model_reasoning`), and `internal/metering`

## Context

Two capabilities are wanted for longer, harder tasks:

1. **Reasoning effort.** People want to choose how hard a model thinks.
   Today no request carries a user choice: z.ai GLM always reasons at a fixed `high` effort, Xiaomi MiMo always reasons (thinking can only be switched off), Claude Opus 5 and Sonnet 5 think by default because no `thinking` parameter is sent, and OpenAI reasoning models run at the API's default effort with no reasoning text captured.
2. **A long-horizon agent loop.** The current loop (`handleAgentLoop`) calls the model until it stops asking for tools, capped at `maxToolCallRounds` (10), and then forces a final answer.
   That suits a chat turn, but not a task that needs dozens of tool calls, a plan, and progress checks along the way, the way coding agents work.

Both must be opt-in additions: nothing changes for anyone who does not select them.

## Decision

### 1. Two settings, opt-in

| Setting | Values | Unset means |
|---|---|---|
| `reasoning_effort` | `off`, `low`, `medium`, `high` | The model's native default, i.e. today's behaviour |
| `agent_loop` | `standard`, `advanced` | `standard`, today's loop |

Only four effort levels are offered.
Providers expose more (`minimal`, `xhigh`, `max`, budgets), but which of those a model accepts varies per model, not just per provider; four levels keep a small, predictable support matrix.

"Unset" is distinct from `off`: unset sends exactly what is sent today, while `off` explicitly asks for no reasoning where the model allows it.

### 2. Where the settings live, and which one wins

The settings follow the existing pattern for a thread's model: they are **copied onto the thread when it is created** and are thread-local from then on.

| Level | Stored on | When it applies |
|---|---|---|
| Account default | `UserPreference.reasoning_effort` / `.agent_loop` | Seeds a new thread when its personality has no value |
| Personality | `Personality.reasoning_effort` / `.agent_loop` | Seeds a new thread (overrides the account default) |
| Thread | `Chat.reasoning_effort` / `.agent_loop` | Every turn in the thread; changed per thread like the model picker |
| Mode | `Mood.reasoning_effort` / `.agent_loop` | Every turn while that mode is active; overrides the thread, **not persisted** onto it, so leaving the mode restores the thread's value |
| Agent job | `AgentJob.reasoning_effort` / `.agent_loop` | That job's runs only; overrides everything, never written to the thread |

So the effective value of a turn is: job → mode → thread (which was seeded from personality → account).
Switching a thread's personality later does not re-seed it, the same as the model.
Every column is nullable; `NULL` means "not set at this level".

When a mode overrides the thread's own setting, the composer shows the effective value and says the mode set it (with a tooltip explaining that the mode's setting wins while it is active).

### 3. Reasoning effort: one capability check, one mapping per provider

Whether a model can take an effort, and how, is decided in code from its provider and name (`models.ReasoningSupportFor`), not stored on the `Model` row, so admin-added models are covered without a migration.
The API exposes it on each model as `reasoning_support`:

| `reasoning_support` | Meaning | UI |
|---|---|---|
| `none` | No effort control | Selector hidden; the setting is ignored |
| `optional` | `off` and the three levels | All four choices |
| `toggle` | Thinking can only be on or off | Off / On (`On` is stored as `medium`) |
| `always` | Always reasons; levels, no `off` | Low / Medium / High; `off` behaves as `low` |

Mapping (`provider` package):

- **OpenAI Responses (GPT-5 family):** `reasoning.effort` = the level; `off` → `none` (`minimal` on the original `gpt-5`/`gpt-5-mini`/`gpt-5-nano`, which do not accept `none`). When the level is not `off`, `reasoning.summary = auto` is requested and the summaries are captured as reasoning text. GPT-4-family models are `none`.
- **Anthropic:** `low`/`medium`/`high` → adaptive thinking with summarized display plus `output_config.effort`; Haiku 4.5, which predates adaptive thinking, gets a thinking budget instead. `off` → thinking disabled on models that accept it (Opus 5, Sonnet 5 and the 5.5 family's equivalents), nothing on models that do not think by default. Models that cannot disable thinking (Opus 5.5, Fable) are `always`. Thinking text is captured, and thinking blocks are replayed across tool rounds when an effort was chosen, as the API requires.
- **z.ai GLM (`always`):** `low` → `low`, `medium` → `high` (today's fixed value), `high` → `max`. The truncation fallback still drops to `low`.
- **Xiaomi MiMo (`toggle`):** `off` disables thinking for the turn; any level leaves it on.
- **Gemini (OpenAI-compatible `reasoning_effort`):** Gemini 3+ and 2.5 Pro always think (`always`); 2.5 Flash can switch it off (`optional`, off → `none`). Thought text is not captured: the compatible endpoint documents no response field for it, and requesting it (`include_thoughts`) cannot be combined with `reasoning_effort`. Thought signatures were already round-tripped.
- **Qwen (DashScope):** Qwen 3.x hybrids and `qwen-plus`/`qwen-flash` are `optional`: off → `enable_thinking: false`; a level turns thinking on with a `thinking_budget` (2k/6k/12k), or on Qwen 3.8 with `reasoning_effort` (low/medium/xhigh). A few thinking-only IDs are `always`.
- **DeepSeek:** `deepseek-flash` / `deepseek-v4-pro` are `optional` via `reasoning_effort`: off → `none` (and `thinking: disabled`), low → `low`, medium → `high`, high → `max`. Thinking is on by default, and DeepSeek requires a tool-calling turn's `reasoning_content` echoed back, so reasoning is captured and replayed on every DeepSeek turn (this fixes a 400 on the second tool round).
- **Mistral:** hybrid models (Small 4, Medium 3.5, Large 4) take only `reasoning_effort` `none`/`high` (`toggle`). While reasoning, Mistral returns content as typed chunks, which the adapter parses (thinking → reasoning, text → reply) and replays as chunks. Aliases whose target is unconfirmed (`mistral-large-latest`) stay `none` until verified.
- DeepSeek, Qwen and Mistral share one adapter core (`reasoningChatAdapter`) that captures, streams and replays reasoning; MiMo keeps its own (truncation fallback).

Capturing OpenAI and Anthropic reasoning follows the z.ai pattern exactly: the adapter implements `ReasoningStreamer`, streams through `reasoningRelay`, and accumulates a `reasoningLog` that becomes `GenerateResponse.Reasoning` and finally `model_reasoning`.
It happens only when the effective effort is not `off`, so turns without an explicit effort are unchanged.

When reasoning is on, the output cap is raised to `provider.ReasoningMaxOutputTokens`, as it already is for GLM and MiMo.
Utility and archival calls (naming, expressions, checkpoints, memory extraction) never take an effort.
`run_subagent` inherits the turn's effort.

### 4. The Advanced loop

The Advanced loop is a **superset** of the Standard loop: an outer loop of *steps*, each of which runs the Standard tool loop with its round counter reset to zero.

```
for step := 0; step < maxSteps; step++ {
    run the inner tool loop (up to maxToolCallRounds rounds)
        └─ inner cap reached → the step simply ends (no forced final answer); the next step continues
    model stopped calling tools:
        no open plan items → this text is the final answer; stop
        open plan items    → append a continuation message listing them; next step
    between steps: cancellation check, quota re-check, context compaction, step bookkeeping
}
outer cap reached → the existing ForceFinalResponse
```

- **Termination is driven by a plan tool.** Advanced turns are offered `update_plan`, a checklist (`pending`, `in_progress`, `completed`, `cancelled`).
  A run ends when the model replies without calling a tool and no item is `pending` or `in_progress`.
  A simple question therefore ends after one step, exactly like Standard; a multi-step task keeps going while it has open work.
  No extra "is it done?" model call is made.
- **Stall guard:** two continuation nudges in a row that produce no tool call and no plan change end the run with the latest text.
- **Limits:** `AGENT_ADVANCED_MAX_STEPS` (default 50, at most 500) and `AGENT_ADVANCED_MAX_DURATION` (default 60 minutes, at most 6 hours). An invalid value falls back to the default and a value above its bound is clamped, each with a warning.
- **Prompting:** an Advanced turn gets a developer instruction (inserted before the final user message) explaining that it may work over many steps, should keep a plan for multi-step work, and that its final reply must stand on its own.

**What is saved.**
Everything the model writes before its final reply (the text between tool calls, earlier steps' replies, and any native reasoning) is treated as reasoning: it streams live into `Job.draft_reasoning` with step headers, and is saved as the message's `model_reasoning`.
The final step's reply is the saved message.
In this first iteration the final reply also streams inside the reasoning box and is promoted to the message body when the run ends.
Tool calls are saved as today, with the step they ran in (`ToolCall.step`), and the live timeline (`ChatTurnProgress`) carries the step number and the current plan.

**Context management.**
The Standard loop never needs to manage context inside a turn; a long run does.
Between steps, when the last call's input exceeds `AGENT_ADVANCED_COMPACT_TOKENS` (default 120k), the run is compacted: a fresh adapter is built from the turn's original `ModelContext` plus a developer "work so far" segment (the goal, the plan, each step's notes and its tool calls with truncated inputs and outputs, newest kept in full first), and continues from there.
Compaction is mechanical, not an extra summarization call, so it is deterministic and costs nothing.
For OpenAI this also starts a new `previous_response_id` chain.

**Adapters.**
Two optional interfaces are added, implemented by every adapter `runGeneration` drives:

- `Continuer.ContinueWith(text)`: records the model's last reply in the adapter's own history and appends a user message, so the next `Call` continues the conversation.
- `UsageReporter.CallUsage()`: per-call input, output and reasoning token counts for every call the adapter made.

The local backend (`LLM_BACKEND=local`) keeps its own pipeline and always runs Standard.

### 5. Metering

`metering.Usage` gains public fields that a deployment's meter may price:

- `ReasoningEffort`: the effective effort when one was explicitly selected and the model honours it; empty otherwise (including always-on models with no selection).
- `AgentLoop`: `standard` or `advanced`.
- `CallInputTokens`: for Advanced turns, the input tokens of **every** model call in the run (interim steps, tool rounds, the final reply), so that each call can be accounted for the same way as a final response.
  Standard turns leave it empty and keep today's accounting (`Tokens`, the final call's input).

The quota is re-checked (`Meter.Check`) between Advanced steps; a denial ends the run with a final answer from what was gathered so far.
`NoopMeter` ignores all of this.

### 6. API and UI

- `GET/PUT /user/preferences`, `PUT /personality/{id}`, `PUT /mood/{id}`, `PATCH /chat/{id}` and the agent-job routes accept and return `reasoning_effort` and `agent_loop`. On the update routes an empty string clears the value (back to "not set"); an omitted field is preserved where the route preserves omitted fields.
- `POST /chat` accepts both, otherwise seeds them as in §2.
- Models carry `reasoning_support`.
- The UI exposes both settings in account settings, the personality editor, the mode editor, the agent-job editor and the composer.

## Consequences

- No behaviour change on release for anyone who does not select a setting.
- Advanced runs can be long and expensive; the step, duration, quota and compaction limits bound them, and a deployment's meter sees every call.
- A thread's settings no longer follow later changes to the account default or the personality, matching the model.
- A queued message behind a long Advanced run still fails after `chatTurnWaitTimeout` (10 minutes); the composer already blocks sending while a turn runs.
- Gemini, Qwen, DeepSeek and Mistral have no effort control yet.

## Alternatives considered

- **Resolving the settings live through the whole hierarchy on every turn.** Rejected for consistency with the model, which is seeded at creation and thread-local after.
- **A `finish` tool, or a separate judge call, to end Advanced runs.** A `finish` tool would carry the final answer as tool arguments, which the draft pipeline cannot stream; a judge call adds cost to every step. The plan checklist gives the loop the state it needs at no extra cost.
- **Summarizing the work log with a model when compacting.** Better summaries, but it adds a provider dependency and a cost per compaction; the mechanical work log is the first step, and a model summary can replace it behind the same seam.
- **Exposing provider-specific effort values.** Rejected for the support-matrix reason in §1.
