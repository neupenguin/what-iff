# Agent-experience (AX) demo: accessibility levels vs. agent success

Does degrading accessibility measurably hurt an AI agent driving the app? This
folder makes the Memory Manager (`/memories`) worse for machines, one
accessibility layer at a time, **without changing a single pixel**. It then
measures how a Claude Code agent driving the browser through Playwright copes,
next to the signals a team would normally watch (axe violations and the
existing role/label-based Playwright specs).

One build serves every level. Pick the level per page load with `?ax=N` (0–4).

## Levels

| Level | Name     | What is removed (features/memory and the ui-modal it opens)                                                                                                                                                                                                                                                                                                                                                                   |
| ----- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0     | baseline | Nothing. The app as shipped.                                                                                                                                                                                                                                                                                                                                                                                                  |
| 1     | no names | `aria-label`s (search, filters, "More actions", "Select memory", "Close", "Starred", …). The filter `<label>` wrappers become `<div>`s, so the visible text stays but is no longer associated with its control.                                                                                                                                                                                                               |
| 2     | no roles | Level 1, plus: the card's ⋮ button and menu items, the status tabs, the bulk-bar actions, the Move menu entries, and the Move/Delete/Cancel dialog footers become `<div (click)>` with no role or tabindex. Checkboxes become styled divs that toggle a class. `ui-modal` drops `role=dialog`/`aria-modal`/`aria-labelledby`, and its close button becomes a div. Cards lose `role=listitem`, and the list loses `role=list`. |
| 3     | no state | Level 2, plus: `aria-selected`, `aria-expanded`, `role=status` and `role=alert` go, so selection state lives only in CSS classes. Key class names (`memory-card`, `memory-card__menu-btn`, `memory-card__menu`, `memories-shell__tab--active`, …) get a random suffix per page load, which breaks CSS-selector scripts between loads.                                                                                         |

| 4 | patterns | Nothing removed. Level 0 plus agent hints: each card's checkbox, ⋮ button and menu are named after the memory they act on; the card's name carries its thread and archived/starred state; the menu button gets `aria-haspopup`/`aria-controls`; the editor textarea is named; bulk actions and Move targets get `aria-description`s; task controls get stable `data-testid`s. |

### Tiers in the evals repo

The [agent-experience-mcpcon26](https://github.com/unlikelyzero/agent-experience-mcpcon26) evals compare three tiers. On what-iff they map to levels of this switch:

| Tier               | Level   | Why                                                                                                                                     |
| ------------------ | ------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| Div soup           | `?ax=3` | No names, no roles, no state, suffixed classes.                                                                                         |
| WCAG / Section 508 | `?ax=0` | The app as shipped already has zero WCAG-tagged axe violations on these screens (only visual `color-contrast` and best-practice rules). |
| 100% patterns      | `?ax=4` | Shipped semantics plus agent hints.                                                                                                     |

Keyboard access degrades along with the markup at level 2+: the divs are not
focusable. That is intended, because it is what this kind of markup does in the wild.

How it works:

- `src/app/core/services/ax-level.service.ts` reads `?ax=N` once at boot (an
  app initializer, so the value survives the login redirect). It keeps the level in
  sessionStorage, so in-app navigation keeps it, and `?ax=0` clears it. With no
  query or session value, it falls back to `localStorage['whatiff.ax-level']`
  (the eval config uses this to pin a level).
- Templates branch with `@if (ax.roles())` and bind attributes through
  `ax.name()` / `ax.role()` / `ax.state()`. All of these return `null` to drop the attribute.
- `features/memory/_ax.scss` holds `ax('class')`, which matches a class in
  both spellings (plain and per-load suffixed), and the `.ax-btn` base styles that
  make a div box like a button. Together they keep the pixels identical.
- `ui-modal` degrades only when a caller passes `[axDegrade]="true"`. Only
  features/memory does, so other modals keep their semantics at every level.

Pixel identity was checked by screenshotting levels 0–3 in eight states:
the list, a selected card, "Select all", the card menu, the Move menu, the
"Move to a thread" dialog, the delete dialog, and the Archived tab. All 24
comparisons against level 0 were byte-identical.

## Tasks

Each run seeds a fresh account (`seed.ts`) with 3 personalities, 4 threads
("Lisbon trip", "Work notes", "Home admin", "Training plan") and 40 memories.
The memories include deliberate distractors. Graders (`tasks.ts`) read every
seeded memory back through the API. A run **passes only if the target changed
as asked AND every other memory is unchanged, field by field** (content,
level, thread, pinned personality, archived, starred).

| Task | Prompt (abridged)                                                | Target                                                            | Distractors                                                                  |
| ---- | ---------------------------------------------------------------- | ----------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| T1   | Star the sister memory from the "Lisbon trip" thread             | "My sister's name is Priya and she lives in Lisbon" (Lisbon trip) | "My sister's name is Priya" (Home admin, **archived**)                       |
| T2   | Change the flight at 9:40 to 10:15, keep the rest of the text    | "Flight to Lisbon is at 9:40 on Friday"                           | "Flight to Chicago … at 7:15 on Monday" (Work notes)                         |
| T3   | Move the seafood-restaurant memory into the "Lisbon trip" thread | "Book dinner at Cervejaria Ramiro …" (Home admin)                 | other Lisbon-trip memories                                                   |
| T4   | Delete every **archived** old-apartment memory, keep the rest    | 3 archived "old apartment" memories                               | "Still owed a deposit refund from the old apartment" (**active**)            |
| T5   | Archive the gym memory and say when done                         | "Gym membership renews on the 1st …"                              | none, but `claim_correct` records whether the agent's "done" matched reality |

The agent is told to end with a line `TASK_COMPLETE` when it believes it is
done. `claimed_done` records that, and `claim_correct` records whether the claim
agrees with the grade. A false "done" (claimed but failed) is the most expensive
failure mode in production, so the summary counts it separately.

## Drivers

| Driver           | Package (pinned)         | How the agent acts                                                                                             |
| ---------------- | ------------------------ | -------------------------------------------------------------------------------------------------------------- |
| `playwright-mcp` | `@playwright/mcp@0.0.83` | The MCP server is the only tool source: `--strict-mcp-config`, built-in tools disabled, headless and isolated. |
| `playwright-cli` | `@playwright/cli@0.1.22` | Bash, restricted to the `playwright-cli` command. Same engine and snapshots, delivered as shell output.        |

Both drivers see the page mainly through Playwright's accessibility-tree
snapshot. That snapshot is exactly what levels 1–3 strip, which is the point of the demo.

## Running it

From the repo root, in three terminals:

```bash
make db-up && make run-mock        # API on :8080, mock LLM, no provider keys
make web                           # Angular dev server on :4200
cd web/app && npm ci               # once: installs tsx and @axe-core/playwright too
```

Then, from `web/app/`:

```bash
# Optional: seed one account by hand and look at it at each level.
npx tsx e2e/ax-demo/seed.ts > /tmp/ax-seed.json
#   then open http://localhost:4200/memories?ax=2 and sign in with the printed username/password

# Agent runs. Needs a signed-in `claude` CLI (or ANTHROPIC_API_KEY). Defaults: all levels,
# both drivers, all tasks, 3 runs each (120 runs). Start small:
npx tsx e2e/ax-demo/run.ts --levels 0,3 --drivers playwright-mcp --tasks T1 --runs 1
npx tsx e2e/ax-demo/run.ts --runs 3 --model sonnet --max-budget-usd 2

# Check the generated commands without calling the model:
npx tsx e2e/ax-demo/run.ts --dry-run --runs 1

# Eval-side signals, one Playwright project per level (Chromium desktop):
npx playwright test --config e2e/ax-demo/playwright.config.ax-levels.ts --project='axe-*'
npx playwright test --config e2e/ax-demo/playwright.config.ax-levels.ts --project='functional-*'

# Tables (Markdown) for the slides; joins in the eval JSON when present.
npx tsx e2e/ax-demo/summarize.ts --json e2e/ax-demo/results/summary.json
```

Runner flags: `--levels`, `--drivers`, `--tasks`, `--runs`, `--app-url`
(default `$E2E_BASE_URL` or `http://localhost:4200`), `--model`,
`--timeout-min` (per run, default 10), `--max-budget-usd` (per run), `--out`,
`--artifacts`, `--browser-executable` / `AX_DEMO_BROWSER` (a pre-installed
Chromium), and `--no-sandbox` / `AX_DEMO_NO_SANDBOX=1` (needed when running as root in a container).

Runs interleave levels, so drift over a long session (rate limits, model load)
spreads evenly instead of landing on one level. Every run keeps its artifacts
in `results/runs/<run-id>/`: `seed.json`, `prompt.txt`, the driver config,
`transcript.jsonl` (raw stream-json) and `grade.json` (with fail reasons).
`results/` is gitignored, and it contains seeded credentials for throwaway local accounts.

The functional projects run the existing `tests/functional/memory` specs
unmodified. Their failures at levels 1–3 are the measurement, not a broken
build, and retries are off so a failure cannot turn into a pass. These projects are not part of `npm run e2e`.

## Metrics

Per run (one CSV row in `results/results.csv`):

| Column                                                                        | Meaning                                                                                                                |
| ----------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `pass`                                                                        | Grader verdict: target changed as asked and nothing else changed.                                                      |
| `claimed_done` / `claim_correct`                                              | Final message had the `TASK_COMPLETE` line / that claim matched `pass`.                                                |
| `tool_calls` / `browser_tool_calls`                                           | `tool_use` blocks in the transcript / those that drove the browser (MCP `browser_*`, or a `playwright-cli` Bash call). |
| `screenshots`                                                                 | `browser_take_screenshot` calls, or `playwright-cli … screenshot`. Agents fall back to these when the tree goes quiet. |
| `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens` | Session totals from the final `result` event. Most input arrives as cache tokens, so read all four.                    |
| `cost_usd`                                                                    | `total_cost_usd` from the `result` event.                                                                              |
| `wall_ms`, `turns`, `exit_code`, `timed_out`                                  | Wall-clock time of the `claude` process, model turns, process exit, whether the per-run timeout killed it.             |
| `fail_reasons`                                                                | The grader's reasons, e.g. `distractor "My sister's name is Priya": starred changed (false -> true)`.                  |

Per level (from `summarize.ts`, also per level × driver and per level × task):

- **success rate**: passes / runs.
- **mean tool calls** and **mean screenshots**, over all runs.
- **tokens per successful task**: all tokens spent at that level, failed runs
  included, divided by the number of passes. This is what one success actually
  costs. It shows `—` when nothing passed. `$ / success` is the same idea using `cost_usd`.
- **false "done"**: runs that claimed `TASK_COMPLETE` but failed the grader.
- **axe violations**: total violation nodes over the three scanned states
  (`results/axe-by-level.json`, which also has per-rule counts).
- **getByRole pass**: pass rate of the memory functional specs at that level
  (`results/functional-by-level.json`, which also lists the failing tests).

## Files

| File                             | Role                                                                   |
| -------------------------------- | ---------------------------------------------------------------------- |
| `seed.ts`                        | Fresh account + personalities, threads, memories; prints JSON.         |
| `tasks.ts`                       | T1–T5 prompts, graders, prompt builder, done-claim check.              |
| `drivers.ts`                     | MCP / CLI driver setup (claude args, env, prompt notes, cleanup).      |
| `stream-metrics.ts`              | Parses `claude -p --output-format stream-json` into run metrics.       |
| `run.ts`                         | The matrix runner; appends to `results/results.csv`.                   |
| `summarize.ts`                   | Per-level aggregates as Markdown tables (and `--json`).                |
| `memories-axe.ax.spec.ts`        | axe scan of `/memories` (populated, Move menu, Move-to-thread dialog). |
| `playwright.config.ax-levels.ts` | `functional-ax0..3` and `axe-ax0..3` projects.                         |
| `ax-level-reporter.ts`           | Writes `functional-by-level.json` and `axe-by-level.json`.             |
