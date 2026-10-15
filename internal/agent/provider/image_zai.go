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

// ZAIImageModel is the z.ai image model the image tool uses.
const ZAIImageModel = "glm-image"

// ZAIPaaSBaseURL is z.ai's general (OpenAI-style) API root on the same host as
// its configured Anthropic-compatible base URL: images live at
// {root}/images/generations. The same layout serves open.bigmodel.cn.
func ZAIPaaSBaseURL(anthropicBase string) string {
	if strings.TrimSpace(anthropicBase) == "" {
		anthropicBase = DefaultZAIBaseURL
	}
	u, err := url.Parse(anthropicBase)
	if err != nil || u.Host == "" {
		return "https://api.z.ai/api/paas/v4"
	}
	return u.Scheme + "://" + u.Host + "/api/paas/v4"
}

// ZAIImageGenerator calls z.ai's images/generations and downloads the result.
//
// The credential is set by the transport's z.ai host rule (x-api-key), which the
// PaaS endpoints accept as well as the Messages API does.
type ZAIImageGenerator struct {
	imageHTTPClient
	baseURL string
	model   string
	tel     *telemetry.Telemetry
}

// NewZAIImageGenerator builds a generator for the PaaS root baseURL (see ZAIPaaSBaseURL).
func NewZAIImageGenerator(client *http.Client, baseURL string, tel *telemetry.Telemetry) *ZAIImageGenerator {
	return &ZAIImageGenerator{imageHTTPClient: imageHTTPClient{client: client}, baseURL: baseURL, model: ZAIImageModel, tel: tel}
}

func (g *ZAIImageGenerator) Provider() models.ModelProvider { return models.ModelProviderZAI }
func (g *ZAIImageGenerator) Model() string                  { return g.model }

type zaiImageRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Size    string `json:"size"`
	Quality string `json:"quality"`
}

type zaiImageResponse struct {
	Data []struct {
		URL string `json:"url"`
	} `json:"data"`
}

// zaiContentFilterCode is z.ai's error code for a prompt or output it refused.
const zaiContentFilterCode = "1301"

func buildZAIImageRequest(model, prompt string, quality ImageQuality, aspectRatio ImageAspectRatio) zaiImageRequest {
	// glm-image's recommended sizes for each shape.
	size := "1280x1280"
	switch aspectRatio {
	case ImageAspectRatioLandscape:
		size = "1568x1056"
	case ImageAspectRatioPortrait:
		size = "1056x1568"
	}
	// hd takes ~20s and standard 5-10s; only high quality pays for hd.
	q := "standard"
	if quality == ImageQualityHigh {
		q = "hd"
	}
	return zaiImageRequest{Model: model, Prompt: prompt, Size: size, Quality: q}
}

func (g *ZAIImageGenerator) GenerateImage(ctx context.Context, prompt string, quality ImageQuality, aspectRatio ImageAspectRatio) (GeneratedImage, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return GeneratedImage{}, fmt.Errorf("prompt is required")
	}
	call := startGenAICall(ctx, g.tel, telemetry.DependencyZAI, g.model, genAIOpGenerateImage)
	var resp zaiImageResponse
	err := g.postJSON(ctx, joinURL(g.baseURL, "images/generations"), nil, buildZAIImageRequest(g.model, prompt, quality, aspectRatio), &resp)
	call.end(err)
	if err != nil {
		return GeneratedImage{}, zaiImageError(err)
	}
	if len(resp.Data) == 0 || strings.TrimSpace(resp.Data[0].URL) == "" {
		return GeneratedImage{}, fmt.Errorf("z.ai returned no image")
	}
	return g.download(ctx, resp.Data[0].URL)
}

// zaiImageError turns z.ai's content-filter refusal into a safety violation.
func zaiImageError(err error) error {
	var apiErr *imageAPIError
	if !errors.As(err, &apiErr) {
		return err
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(apiErr.Body), &body) == nil && body.Error.Code == zaiContentFilterCode {
		return imageSafetyViolation(models.SafetyViolationProviderZAI, body.Error.Code, body.Error.Message, apiErr.Body)
	}
	return err
}
