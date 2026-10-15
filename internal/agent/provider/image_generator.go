package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// GeneratedImage is one image produced by an ImageGenerator.
type GeneratedImage struct {
	// Base64 is the raw base64 payload (no data: prefix).
	Base64 string
	// MIMEType is the image's actual format. Vendors do not all return PNG
	// (Gemini answers with JPEG), so callers must not assume one.
	MIMEType string
}

// ImageGenerator is one vendor's text-to-image endpoint. The generate_image tool
// and the image ritual use whichever one the user picked (UserPreference
// image_generation_provider); see Agent.imageGeneratorForUser.
//
// Quality and aspect ratio are the tool's vendor-neutral knobs. Each adapter maps
// them to the closest thing its API has and ignores what it cannot express.
type ImageGenerator interface {
	// Provider is the vendor this generator calls.
	Provider() models.ModelProvider
	// Model is the vendor model id, reported to metering as the engine.
	Model() string
	GenerateImage(ctx context.Context, prompt string, quality ImageQuality, aspectRatio ImageAspectRatio) (GeneratedImage, error)
}

// ImageGenerationProviders lists the vendors an ImageGenerator exists for, in the
// order the settings UI offers them. OpenAI is first because it is the default.
var ImageGenerationProviders = []models.ModelProvider{
	models.ModelProviderOpenAI,
	models.ModelProviderGoogle,
	models.ModelProviderZAI,
	models.ModelProviderQwen,
}

// IsImageGenerationProvider reports whether p has an ImageGenerator.
func IsImageGenerationProvider(p models.ModelProvider) bool {
	for _, known := range ImageGenerationProviders {
		if known == p {
			return true
		}
	}
	return false
}

// openAIImageGenerator adapts OpenAIProvider's Images API calls to ImageGenerator.
type openAIImageGenerator struct{ p *OpenAIProvider }

// ImageGenerator returns the OpenAI Images API (ImageEngine) as an ImageGenerator.
func (a *OpenAIProvider) ImageGenerator() ImageGenerator { return openAIImageGenerator{p: a} }

func (g openAIImageGenerator) Provider() models.ModelProvider { return models.ModelProviderOpenAI }
func (g openAIImageGenerator) Model() string                  { return ImageEngine }

func (g openAIImageGenerator) GenerateImage(ctx context.Context, prompt string, quality ImageQuality, aspectRatio ImageAspectRatio) (GeneratedImage, error) {
	b64, err := g.p.GenerateImagePNGBase64WithOptions(ctx, prompt, quality, aspectRatio)
	if err != nil {
		return GeneratedImage{}, err
	}
	return GeneratedImage{Base64: b64, MIMEType: "image/png"}, nil
}

// maxDownloadedImageBytes caps an image fetched from a vendor's result URL.
const maxDownloadedImageBytes = 32 << 20

// imageHTTPClient is the plumbing the HTTP-only adapters share: a JSON POST and,
// for vendors that answer with a URL, a download of the result.
type imageHTTPClient struct {
	client *http.Client
}

func (c imageHTTPClient) httpClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	return http.DefaultClient
}

// postJSON POSTs body to url and decodes a 2xx response into out. A non-2xx
// response is returned as *imageAPIError carrying the body, so adapters can
// recognise their vendor's content-policy refusals.
func (c imageHTTPClient) postJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode image request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build image request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadedImageBytes*2))
	if err != nil {
		return fmt.Errorf("read image response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &imageAPIError{Status: resp.StatusCode, Body: string(raw)}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode image response: %w", err)
	}
	return nil
}

// download fetches a result image and returns it base64-encoded with its
// sniffed MIME type. Result URLs are pre-signed, so no credential is sent.
func (c imageHTTPClient) download(ctx context.Context, url string) (GeneratedImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return GeneratedImage{}, fmt.Errorf("build image download: %w", err)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return GeneratedImage{}, fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return GeneratedImage{}, fmt.Errorf("download image: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadedImageBytes+1))
	if err != nil {
		return GeneratedImage{}, fmt.Errorf("download image: %w", err)
	}
	if len(data) > maxDownloadedImageBytes {
		return GeneratedImage{}, fmt.Errorf("download image: larger than %d bytes", maxDownloadedImageBytes)
	}
	return imageFromBytes(data)
}

// imageFromBytes checks that data is an image and encodes it.
func imageFromBytes(data []byte) (GeneratedImage, error) {
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return GeneratedImage{}, fmt.Errorf("image response is %s, not an image", mime)
	}
	return GeneratedImage{Base64: base64.StdEncoding.EncodeToString(data), MIMEType: mime}, nil
}

// imageAPIError is a non-2xx answer from an image endpoint.
type imageAPIError struct {
	Status int
	Body   string
}

func (e *imageAPIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 500 {
		body = body[:500] + "…"
	}
	return fmt.Sprintf("image API returned status %d: %s", e.Status, body)
}

// imageSafetyViolation builds the typed refusal the generate_image tool records
// as a safety event, for vendors whose refusal text the generic sniffer
// (ParseSafetyViolationError) does not know.
func imageSafetyViolation(p models.SafetyViolationProvider, code, message, raw string) error {
	if strings.TrimSpace(message) == "" {
		message = "The image request was blocked by the provider's content policy."
	}
	return &SafetyViolationError{Violation: &SafetyViolation{
		Provider:        p,
		ViolationType:   "content_policy",
		ProviderCode:    code,
		ProviderMessage: message,
		RawError:        raw,
	}}
}

// joinURL appends path to base with exactly one slash between them.
func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}
