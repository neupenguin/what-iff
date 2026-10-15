package agent

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

var retiredImageRitualID = uuid.MustParse("00000000-0000-0000-0000-000000000101")

func TestDropRetiredRituals(t *testing.T) {
	t.Parallel()
	imageGen := &models.Ritual{ID: retiredImageRitualID, Name: "Generate Image"}
	skill := &models.Ritual{ID: uuid.New(), Name: "Skill"}

	require.Nil(t, dropRetiredRituals(nil))
	kept := []*models.Ritual{skill}
	require.Equal(t, kept, dropRetiredRituals(kept))
	require.Equal(t, []*models.Ritual{skill}, dropRetiredRituals([]*models.Ritual{imageGen, nil, skill}))
	require.Empty(t, dropRetiredRituals([]*models.Ritual{imageGen}))
}

func TestMergedRitualIDsForTools_SkipsRetiredRituals(t *testing.T) {
	t.Parallel()
	skill := uuid.New()
	msg := &models.ChatMessage{Rituals: []*models.Ritual{{ID: retiredImageRitualID}, {ID: skill}}}
	require.Equal(t, []uuid.UUID{skill}, mergedRitualIDsForTools(msg, nil))
}
