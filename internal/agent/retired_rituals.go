package agent

import (
	"github.com/google/uuid"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// retiredRitualIDs are code-defined "system rituals" that no longer exist. The only
// one was image generation, which the generate_image tool replaced. A client loaded
// before its removal can still send its ID with a message; it has no ritual row, so
// it is dropped rather than linked.
var retiredRitualIDs = map[uuid.UUID]struct{}{
	uuid.MustParse("00000000-0000-0000-0000-000000000101"): {}, // image generation
}

// dropRetiredRituals returns rituals without nil entries and retired system
// rituals, reusing in when there is nothing to drop.
func dropRetiredRituals(in []*models.Ritual) []*models.Ritual {
	keep := func(r *models.Ritual) bool {
		if r == nil {
			return false
		}
		_, retired := retiredRitualIDs[r.ID]
		return !retired
	}
	clean := true
	for _, r := range in {
		if !keep(r) {
			clean = false
			break
		}
	}
	if clean {
		return in
	}
	out := make([]*models.Ritual, 0, len(in))
	for _, r := range in {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}
