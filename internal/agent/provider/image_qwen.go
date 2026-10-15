package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/telemetry"
)

// QwenImageModel is the Qwen-Image model the image tool uses.
const QwenImageModel = "qwen-image-2.0"

// QwenNativeBaseURL is the DashScope native API root on the same host as the
// configured compatible-mode base URL, so a China-region QWEN_BASE_URL moves
// image generation to the China region too.
func QwenNativeBaseURL(compatBase string) string {
	if strings.TrimSpace(compatBase) == "" {
		compatBase = DefaultQwenBaseURL
	}
	u, err := url.Parse(compatBase)
	if err != nil || u.Host == "" {
		return "https://dashscope-intl.aliyuncs.com/api/v1"
	}
	return u.Scheme + "://" + u.Host + "/api/v1"
}

// QwenImageGenerator calls DashScope's synchronous multimodal-generation
// endpoint and downloads the result (its URL expires after 24 hours).
//
// The credential is set by the transport's Qwen host rule (Bearer).
type QwenImageGenerator struct {
	imageHTTPClient
	baseURL string
	model   string
	tel     *telemetry.Telemetry
}

// NewQwenImageGenerator builds a generator for the native root baseURL (see QwenNativeBaseURL).
func NewQwenImageGenerator(client *http.Client, baseURL string, tel *telemetry.Telemetry) *QwenImageGenerator {
	return &QwenImageGenerator{imageHTTPClient: imageHTTPClient{client: client}, baseURL: baseURL, model: QwenImageModel, tel: tel}
}

func (g *QwenImageGenerator) Provider() models.ModelProvider { return models.ModelProviderQwen }
func (g *QwenImageGenerator) Model() string                  { return g.model }

type qwenImageRequest struct {
	Model      string              `json:"model"`
	Input      qwenImageInput      `json:"input"`
	Parameters qwenImageParameters `json:"parameters"`
}

type qwenImageInput struct {
	Messages []qwenImageMessage `json:"messages"`
}

type qwenImageMessage struct {
	Role    string              `json:"role"`
	Content []map[string]string `json:"content"`
}

type qwenImageParameters struct {
	Size         string `json:"size"`
	N            int    `json:"n"`
	Watermark    bool   `json:"watermark"`
	PromptExtend bool   `json:"prompt_extend"`
}

type qwenImageResponse struct {
	Output struct {
		Choices []struct {
			Message struct {
				Content []struct {
					Image string `json:"image"`
				} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	} `json:"output"`
}

// qwenContentFilterCodes are DashScope's error codes for refused content.
var qwenContentFilterCodes = map[string]bool{
	"DataInspectionFailed":   true,
	"data_inspection_failed": true,
	"IPInfringementSuspect":  true,
}

func buildQwenImageRequest(model, prompt string, quality ImageQuality, aspectRatio ImageAspectRatio) qwenImageRequest {
	// Same pixel shapes as the OpenAI path; high quality doubles each side.
	w, h := 1024, 1024
	switch aspectRatio {
	case ImageAspectRatioLandscape:
		w, h = 1536, 1024
	case ImageAspectRatioPortrait:
		w, h = 1024, 1536
	}
	if quality == ImageQualityHigh {
		// The model's ceiling is 2048*2048 total pixels.
		if w == h {
			w, h = 2048, 2048
		} else if w > h {
			w, h = 2048, 1365
		} else {
			w, h = 1365, 2048
		}
	}
	return qwenImageRequest{
		Model: model,
		Input: qwenImageInput{Messages: []qwenImageMessage{{
			Role:    "user",
			Content: []map[string]string{{"text": prompt}},
		}}},
		// The prompt was already written by the chat model; DashScope's rewrite
		// would second-guess it.
		Parameters: qwenImageParameters{Size: fmt.Sprintf("%d*%d", w, h), N: 1, Watermark: false, PromptExtend: false},
	}
}

func (g *QwenImageGenerator) GenerateImage(ctx context.Context, prompt string, quality ImageQuality, aspectRatio ImageAspectRatio) (GeneratedImage, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return GeneratedImage{}, fmt.Errorf("prompt is required")
	}
	call := startGenAICall(ctx, g.tel, telemetry.DependencyQwen, g.model, genAIOpGenerateImage)
	var resp qwenImageResponse
	endpoint := joinURL(g.baseURL, "services/aigc/multimodal-generation/generation")
	err := g.postJSON(ctx, endpoint, nil, buildQwenImageRequest(g.model, prompt, quality, aspectRatio), &resp)
	call.end(err)
	if err != nil {
		return GeneratedImage{}, qwenImageError(err)
	}
	for _, c := range resp.Output.Choices {
		for _, part := range c.Message.Content {
			if strings.TrimSpace(part.Image) != "" {
				return g.download(ctx, part.Image)
			}
		}
	}
	return GeneratedImage{}, fmt.Errorf("qwen returned no image")
}

// qwenImageError turns DashScope's content-inspection refusal into a safety violation.
func qwenImageError(err error) error {
	var apiErr *imageAPIError
	if !errors.As(err, &apiErr) {
		return err
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(apiErr.Body), &body) == nil && qwenContentFilterCodes[body.Code] {
		return imageSafetyViolation(models.SafetyViolationProviderQwen, body.Code, body.Message, apiErr.Body)
	}
	return err
}
