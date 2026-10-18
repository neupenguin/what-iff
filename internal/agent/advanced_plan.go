package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	agenttools "github.com/theimaginaryfoundation/what-iff/internal/agent/tools"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// turnOnlyToolSpecs are tools a turn offers outside the catalog: update_plan on Advanced turns
// (ADR 0x026), where the plan is what tells the loop whether work remains.
func turnOnlyToolSpecs(chatCtx *chatContext) []agenttools.FunctionToolSpec {
	if chatCtx == nil || !chatCtx.turnSettings.Advanced() {
		return nil
	}
	return []agenttools.FunctionToolSpec{agenttools.UpdatePlanToolSpec}
}

// updatePlanTool replaces the run's plan and publishes it to the live timeline.
func updatePlanTool(chatCtx *chatContext, input []byte) (string, error) {
	plan, err := agenttools.ParseUpdatePlanArgs(input)
	if err != nil {
		return "", err
	}
	chatCtx.plan = plan
	chatCtx.planVersion++
	chatCtx.toolProgress.SetPlan(plan)
	out, err := json.Marshal(map[string]any{
		"success":    true,
		"open_items": len(openPlanItems(plan)),
		"total":      len(plan),
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// openPlanItems returns the items still pending or in progress.
func openPlanItems(plan []models.PlanItem) []models.PlanItem {
	var open []models.PlanItem
	for _, item := range plan {
		if item.Status.Open() {
			open = append(open, item)
		}
	}
	return open
}

// formatPlan renders a plan as a checklist for the model.
func formatPlan(plan []models.PlanItem) string {
	var b strings.Builder
	for _, item := range plan {
		fmt.Fprintf(&b, "- [%s] %s\n", item.Status, item.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}
