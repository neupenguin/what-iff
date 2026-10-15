package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestGenerateImageTool_BillsTheChosenVendor(t *testing.T) {
	t.Parallel()
	meter := &recordingMeter{allow: true}
	a := &Agent{logger: zap.NewNop(), meter: meter, imageGenerators: map[models.ModelProvider]provider.ImageGenerator{
		models.ModelProviderOpenAI: &fakeImageGenerator{provider: models.ModelProviderOpenAI},
		models.ModelProviderZAI: &fakeImageGenerator{
			provider: models.ModelProviderZAI,
			image:    provider.GeneratedImage{Base64: "/9j/4AAQ", MIMEType: "image/jpeg"},
		},
	}}
	a.testHooks.ImageGenerationPreference = preferring(models.ModelProviderZAI)

	quality := "high"
	args, err := json.Marshal(generateImageToolArgs{Prompt: "a fox", Quality: &quality})
	require.NoError(t, err)
	_, _, err = a.generateImageTool(context.Background(), &models.Chat{ID: uuid.New(), UserID: uuid.New()}, args)
	require.NoError(t, err)

	require.Len(t, meter.records, 1)
	u := meter.records[0]
	require.Equal(t, "zai", u.ImageProvider)
	require.Equal(t, "high", u.ImageQuality)
	require.Equal(t, "zai", u.Metadata["provider"])
	require.Equal(t, "zai-image", u.Metadata["engine"])
}
