package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"go.uber.org/zap"
)

// fakeImageGenerator is an ImageGenerator that returns a fixed image.
type fakeImageGenerator struct {
	provider models.ModelProvider
	image    provider.GeneratedImage
	err      error
	calls    atomic.Int32
}

func (f *fakeImageGenerator) Provider() models.ModelProvider { return f.provider }
func (f *fakeImageGenerator) Model() string                  { return string(f.provider) + "-image" }
func (f *fakeImageGenerator) GenerateImage(context.Context, string, provider.ImageQuality, provider.ImageAspectRatio) (provider.GeneratedImage, error) {
	f.calls.Add(1)
	return f.image, f.err
}

func preferring(p models.ModelProvider) func(context.Context, uuid.UUID) (models.ModelProvider, error) {
	return func(context.Context, uuid.UUID) (models.ModelProvider, error) { return p, nil }
}

func TestImageGeneratorForUser(t *testing.T) {
	t.Parallel()
	openAI := &fakeImageGenerator{provider: models.ModelProviderOpenAI}
	gemini := &fakeImageGenerator{provider: models.ModelProviderGoogle}
	registry := map[models.ModelProvider]provider.ImageGenerator{
		models.ModelProviderOpenAI: openAI,
		models.ModelProviderGoogle: gemini,
	}

	t.Run("uses the vendor the user picked", func(t *testing.T) {
		a := &Agent{logger: zap.NewNop(), imageGenerators: registry}
		a.testHooks.ImageGenerationPreference = preferring(models.ModelProviderGoogle)
		gen, err := a.imageGeneratorForUser(context.Background(), uuid.New())
		require.NoError(t, err)
		require.Same(t, gemini, gen)
	})

	t.Run("no pick means OpenAI", func(t *testing.T) {
		a := &Agent{logger: zap.NewNop(), imageGenerators: registry}
		a.testHooks.ImageGenerationPreference = preferring("")
		gen, err := a.imageGeneratorForUser(context.Background(), uuid.New())
		require.NoError(t, err)
		require.Same(t, openAI, gen)
	})

	t.Run("a pick this deployment cannot serve falls back to OpenAI", func(t *testing.T) {
		a := &Agent{logger: zap.NewNop(), imageGenerators: registry}
		a.testHooks.ImageGenerationPreference = preferring(models.ModelProviderQwen)
		gen, err := a.imageGeneratorForUser(context.Background(), uuid.New())
		require.NoError(t, err)
		require.Same(t, openAI, gen)
	})

	t.Run("an unreadable preference falls back to OpenAI", func(t *testing.T) {
		a := &Agent{logger: zap.NewNop(), imageGenerators: registry}
		a.testHooks.ImageGenerationPreference = func(context.Context, uuid.UUID) (models.ModelProvider, error) {
			return "", errors.New("db down")
		}
		gen, err := a.imageGeneratorForUser(context.Background(), uuid.New())
		require.NoError(t, err)
		require.Same(t, openAI, gen)
	})

	t.Run("one configured vendor is used without a preference read", func(t *testing.T) {
		a := &Agent{logger: zap.NewNop(), imageGenerators: map[models.ModelProvider]provider.ImageGenerator{models.ModelProviderGoogle: gemini}}
		a.testHooks.ImageGenerationPreference = func(context.Context, uuid.UUID) (models.ModelProvider, error) {
			t.Fatal("preference read with nothing to choose between")
			return "", nil
		}
		gen, err := a.imageGeneratorForUser(context.Background(), uuid.New())
		require.NoError(t, err)
		require.Same(t, gemini, gen, "a Gemini-only deployment still generates images")
	})

	t.Run("nothing configured is an error", func(t *testing.T) {
		a := &Agent{logger: zap.NewNop(), imageGenerators: map[models.ModelProvider]provider.ImageGenerator{}}
		_, err := a.imageGeneratorForUser(context.Background(), uuid.New())
		require.Error(t, err)
	})

	t.Run("an agent built without a registry uses its OpenAIProvider", func(t *testing.T) {
		a := &Agent{logger: zap.NewNop(), OpenAIProvider: newNoRetryTestOpenAIProvider("http://127.0.0.1:1")}
		gen, err := a.imageGeneratorForUser(context.Background(), uuid.New())
		require.NoError(t, err)
		require.Equal(t, models.ModelProviderOpenAI, gen.Provider())
		require.Equal(t, provider.ImageEngine, gen.Model())
	})
}

func TestGenerateImageTool_SavesTheVendorsImageFormat(t *testing.T) {
	t.Parallel()
	gemini := &fakeImageGenerator{
		provider: models.ModelProviderGoogle,
		image:    provider.GeneratedImage{Base64: "/9j/4AAQ", MIMEType: "image/jpeg"},
	}
	a := &Agent{logger: zap.NewNop(), imageGenerators: map[models.ModelProvider]provider.ImageGenerator{
		models.ModelProviderOpenAI: &fakeImageGenerator{provider: models.ModelProviderOpenAI},
		models.ModelProviderGoogle: gemini,
	}}
	a.testHooks.ImageGenerationPreference = preferring(models.ModelProviderGoogle)

	count := 2
	args, err := json.Marshal(generateImageToolArgs{Prompt: "a fox", Count: &count})
	require.NoError(t, err)
	out, atts, err := a.generateImageTool(context.Background(), &models.Chat{ID: uuid.New(), UserID: uuid.New()}, args)
	require.NoError(t, err)
	require.EqualValues(t, 2, gemini.calls.Load())
	require.Len(t, atts, 2)
	for _, att := range atts {
		require.Equal(t, "image/jpeg", att.FileType)
		require.True(t, strings.HasSuffix(att.Name, ".jpg"), att.Name)
		require.Equal(t, "/9j/4AAQ", att.FileContent)
	}
	var result generateImageToolResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.True(t, result.Success)
}

func TestGenerateImageTool_AttributesAVendorRefusal(t *testing.T) {
	t.Parallel()
	refusal := &provider.SafetyViolationError{Violation: &provider.SafetyViolation{
		Provider: models.SafetyViolationProviderQwen, ProviderCode: "DataInspectionFailed", ProviderMessage: "blocked",
	}}
	gen := &fakeImageGenerator{provider: models.ModelProviderQwen, err: refusal}
	v, ok := imageSafetyViolationFor(gen, refusal)
	require.True(t, ok)
	require.Equal(t, models.SafetyViolationProviderQwen, v.Provider)

	// OpenAI's refusals arrive as API errors and are recognised by text, attributed to the generator's vendor.
	v, ok = imageSafetyViolationFor(&fakeImageGenerator{provider: models.ModelProviderOpenAI}, errors.New(`{"error":{"code":"moderation_blocked","message":"Your request was rejected by the safety system."}}`))
	require.True(t, ok)
	require.Equal(t, models.SafetyViolationProviderOpenAI, v.Provider)

	_, ok = imageSafetyViolationFor(gen, errors.New("connection reset"))
	require.False(t, ok)
}

func TestGeneratedImageFileType(t *testing.T) {
	t.Parallel()
	cases := map[string][2]string{
		"image/png":   {"image/png", ".png"},
		"IMAGE/JPEG ": {"image/jpeg", ".jpg"},
		"image/webp":  {"image/webp", ".webp"},
		"":            {"image/png", ".png"},
		"image/heic":  {"image/png", ".png"},
	}
	for in, want := range cases {
		mime, ext := generatedImageFileType(in)
		require.Equal(t, want, [2]string{mime, ext}, in)
	}
}
