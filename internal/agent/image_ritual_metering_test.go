package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	"github.com/theimaginaryfoundation/what-iff/internal/metering"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestHandleImageGenerateRitual_RemembersTheVendor(t *testing.T) {
	t.Parallel()
	chatID := uuid.New()
	userMsg := &models.ChatMessage{ChatID: chatID, Message: "a red balloon"}
	chatCtx := &chatContext{model: models.DefaultModelName, chat: &models.Chat{ID: chatID}}
	mc := &provider.ModelContext{}
	mc.Append(provider.SegmentKindUserMessage, provider.RoleUser, "context", false)

	qwen := &fakeImageGenerator{provider: models.ModelProviderQwen}
	a := &Agent{logger: zap.NewNop(), imageGenerators: map[models.ModelProvider]provider.ImageGenerator{
		models.ModelProviderOpenAI: &fakeImageGenerator{provider: models.ModelProviderOpenAI},
		models.ModelProviderQwen:   qwen,
	}}
	a.testHooks = agentTestHooks{
		ImageGenerationPreference: preferring(models.ModelProviderQwen),
		ImageRitualOpenAIInfer: func(context.Context, responses.ResponseNewParams) (string, *provider.GenerateResponse, error) {
			return "a prompt", &provider.GenerateResponse{ID: "resp"}, nil
		},
		ImageRitualCreateChatMessage: func(_ context.Context, _ uuid.UUID, cm models.ChatMessage) (*models.ChatMessage, error) {
			cm.ID = uuid.New()
			return &cm, nil
		},
		ImageRitualCreateFileAttachment: func(_ context.Context, _ uuid.UUID, fa models.FileAttachment) (*models.FileAttachment, error) {
			fa.ID = uuid.New()
			return &fa, nil
		},
		ImageRitualPersistImage: func(context.Context, uuid.UUID, *models.FileAttachment, string) error { return nil },
	}

	_, _, err := a.handleImageGenerateRitual(context.Background(), uuid.New(), userMsg, chatCtx, mc)
	require.NoError(t, err)
	require.EqualValues(t, 1, qwen.calls.Load())
	require.Same(t, qwen, chatCtx.ritualImageGenerator, "the turn's usage is recorded against the vendor that drew it")
}

func TestRitualImageUsage(t *testing.T) {
	t.Parallel()

	t.Run("records the vendor and keeps the turn's metadata", func(t *testing.T) {
		t.Parallel()
		cancelled := map[string]interface{}{"cancelled": true}
		usage := metering.Usage{ActionType: models.ActionTypeImageGeneration, Metadata: cancelled}
		ritualImageUsage(&usage, &chatContext{ritualImageGenerator: &fakeImageGenerator{provider: models.ModelProviderGoogle}})

		require.Equal(t, "google", usage.ImageProvider)
		require.Equal(t, map[string]interface{}{
			"cancelled": true,
			"path":      "ritual",
			"engine":    "google-image",
			"provider":  "google",
		}, usage.Metadata)
		require.Len(t, cancelled, 1, "the caller's map is not modified")
		require.Empty(t, usage.ImageQuality, "a ritual is still priced as the fixed ritual image")
	})

	t.Run("a ritual that never reached a vendor is left alone", func(t *testing.T) {
		t.Parallel()
		usage := metering.Usage{ActionType: models.ActionTypeImageGeneration}
		ritualImageUsage(&usage, &chatContext{})
		ritualImageUsage(&usage, nil)
		require.Empty(t, usage.ImageProvider)
		require.Nil(t, usage.Metadata)
	})
}

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
