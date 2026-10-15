package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/telemetry"
)

// GeminiImageModel is the Gemini image model ("Nano Banana 2") the image tool uses.
const GeminiImageModel = "gemini-3.1-flash-image"

// GeminiNativeBaseURL is the native Gemini API root for a configured
// OpenAI-compatible Gemini base URL: ".../v1beta/openai/" becomes ".../v1beta".
// Image generation is only on the native API, at {root}/models/{model}:generateContent.
func GeminiNativeBaseURL(openAICompatBase string) string {
	if strings.TrimSpace(openAICompatBase) == "" {
		openAICompatBase = DefaultGeminiBaseURL
	}
	return strings.TrimSuffix(strings.TrimRight(openAICompatBase, "/"), "/openai")
}

// GeminiImageGenerator calls Gemini's native generateContent with image output.
//
// The key travels as x-goog-api-key, set by the credential transport's
// path-scoped Gemini rule (see GoogleAPIKey). Google answers 401 when an API key
// also arrives as a Bearer token, so the adapter sets no auth header itself.
type GeminiImageGenerator struct {
	imageHTTPClient
	baseURL string
	model   string
	tel     *telemetry.Telemetry
}

// NewGeminiImageGenerator builds a generator for the native API root baseURL
// (see GeminiNativeBaseURL). client should be the shared credentialed client.
func NewGeminiImageGenerator(client *http.Client, baseURL string, tel *telemetry.Telemetry) *GeminiImageGenerator {
	return &GeminiImageGenerator{imageHTTPClient: imageHTTPClient{client: client}, baseURL: baseURL, model: GeminiImageModel, tel: tel}
}

func (g *GeminiImageGenerator) Provider() models.ModelProvider { return models.ModelProviderGoogle }
func (g *GeminiImageGenerator) Model() string                  { return g.model }

type geminiImageRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
	Thought    bool              `json:"thought,omitempty"`
}

type geminiInlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiGenerationConfig struct {
	ResponseModalities []string          `json:"responseModalities"`
	ImageConfig        geminiImageConfig `json:"imageConfig"`
}

type geminiImageConfig struct {
	AspectRatio string `json:"aspectRatio"`
	ImageSize   string `json:"imageSize"`
}

type geminiImageResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

// geminiBlockedFinishReasons are the finish reasons that mean the image was
// refused on content grounds rather than failing.
var geminiBlockedFinishReasons = map[string]bool{
	"SAFETY":                   true,
	"IMAGE_SAFETY":             true,
	"PROHIBITED_CONTENT":       true,
	"BLOCKLIST":                true,
	"SPII":                     true,
	"IMAGE_PROHIBITED_CONTENT": true,
}

func buildGeminiImageRequest(prompt string, quality ImageQuality, aspectRatio ImageAspectRatio) geminiImageRequest {
	ratio := "1:1"
	switch aspectRatio {
	case ImageAspectRatioLandscape:
		ratio = "3:2"
	case ImageAspectRatioPortrait:
		ratio = "2:3"
	}
	// 1K is the model's base size; high quality buys the 2K render.
	size := "1K"
	if quality == ImageQualityHigh {
		size = "2K"
	}
	return geminiImageRequest{
		Contents: []geminiContent{{Role: "user", Parts: []geminiPart{{Text: prompt}}}},
		GenerationConfig: geminiGenerationConfig{
			ResponseModalities: []string{"IMAGE"},
			ImageConfig:        geminiImageConfig{AspectRatio: ratio, ImageSize: size},
		},
	}
}

func (g *GeminiImageGenerator) GenerateImage(ctx context.Context, prompt string, quality ImageQuality, aspectRatio ImageAspectRatio) (GeneratedImage, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return GeneratedImage{}, fmt.Errorf("prompt is required")
	}
	endpoint := joinURL(g.baseURL, "models/"+g.model+":generateContent")
	call := startGenAICall(ctx, g.tel, telemetry.DependencyGemini, g.model, genAIOpGenerateImage)
	var resp geminiImageResponse
	err := g.postJSON(ctx, endpoint, nil, buildGeminiImageRequest(prompt, quality, aspectRatio), &resp)
	if err == nil {
		call.recordUsage(genAIUsage{Input: resp.UsageMetadata.PromptTokenCount, Output: resp.UsageMetadata.CandidatesTokenCount})
	}
	call.end(err)
	if err != nil {
		return GeneratedImage{}, err
	}
	return geminiImageFromResponse(resp)
}

// geminiImageFromResponse takes the last non-thought image part. Thinking
// models can emit draft images marked thought:true before the final one.
func geminiImageFromResponse(resp geminiImageResponse) (GeneratedImage, error) {
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		raw, _ := json.Marshal(resp.PromptFeedback)
		return GeneratedImage{}, imageSafetyViolation(models.SafetyViolationProviderGoogle, resp.PromptFeedback.BlockReason, "", string(raw))
	}
	var image *geminiInlineData
	var finish, text string
	for _, c := range resp.Candidates {
		finish = c.FinishReason
		for _, p := range c.Content.Parts {
			switch {
			case p.Thought:
			case p.InlineData != nil && p.InlineData.Data != "":
				image = p.InlineData
			case p.Text != "":
				text = p.Text
			}
		}
	}
	if image != nil {
		mime := image.MIMEType
		if mime == "" {
			mime = "image/png"
		}
		return GeneratedImage{Base64: image.Data, MIMEType: mime}, nil
	}
	if geminiBlockedFinishReasons[finish] {
		return GeneratedImage{}, imageSafetyViolation(models.SafetyViolationProviderGoogle, finish, text, "finishReason="+finish)
	}
	if text != "" {
		return GeneratedImage{}, fmt.Errorf("gemini returned no image (finishReason=%s): %s", finish, text)
	}
	return GeneratedImage{}, fmt.Errorf("gemini returned no image (finishReason=%s)", finish)
}
