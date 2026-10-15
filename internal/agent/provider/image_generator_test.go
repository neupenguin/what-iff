package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func testImageBytes(t *testing.T, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		require.NoError(t, jpeg.Encode(&buf, img, nil))
	default:
		require.NoError(t, png.Encode(&buf, img))
	}
	return buf.Bytes()
}

// fakeImageVendor records the generation request and serves result images at /files/.
type fakeImageVendor struct {
	t        *testing.T
	srv      *httptest.Server
	lastPath string
	lastBody map[string]any
	respond  func(w http.ResponseWriter)
	file     []byte
}

func newFakeImageVendor(t *testing.T) *fakeImageVendor {
	f := &fakeImageVendor{t: t}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && len(r.URL.Path) > len("/files/") && r.URL.Path[:len("/files/")] == "/files/" {
			_, _ = w.Write(f.file)
			return
		}
		f.lastPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		f.lastBody = map[string]any{}
		require.NoError(t, json.Unmarshal(raw, &f.lastBody))
		f.respond(w)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func requireSafetyViolation(t *testing.T, err error, p models.SafetyViolationProvider, code string) {
	t.Helper()
	var sve *SafetyViolationError
	require.True(t, errors.As(err, &sve), "want a SafetyViolationError, got %v", err)
	require.Equal(t, p, sve.Violation.Provider)
	require.Equal(t, code, sve.Violation.ProviderCode)
}

func TestImageGenerationProviders_AreKnownVendors(t *testing.T) {
	require.Equal(t, models.ModelProviderOpenAI, ImageGenerationProviders[0], "OpenAI is the default and listed first")
	for _, p := range ImageGenerationProviders {
		require.True(t, IsImageGenerationProvider(p))
	}
	require.False(t, IsImageGenerationProvider(models.ModelProviderDeepSeek))
}

func TestImageBaseURLsFollowTheConfiguredChatEndpoints(t *testing.T) {
	require.Equal(t, "https://generativelanguage.googleapis.com/v1beta", GeminiNativeBaseURL(""))
	require.Equal(t, "https://proxy.example/v1beta", GeminiNativeBaseURL("https://proxy.example/v1beta/openai/"))

	require.Equal(t, "https://api.z.ai/api/paas/v4", ZAIPaaSBaseURL(""))
	require.Equal(t, "https://open.bigmodel.cn/api/paas/v4", ZAIPaaSBaseURL("https://open.bigmodel.cn/api/anthropic"))

	require.Equal(t, "https://dashscope-intl.aliyuncs.com/api/v1", QwenNativeBaseURL(""))
	require.Equal(t, "https://dashscope.aliyuncs.com/api/v1", QwenNativeBaseURL("https://dashscope.aliyuncs.com/compatible-mode/v1"), "a China-region key keeps images in the China region")
}

func TestGeminiImageGenerator(t *testing.T) {
	jpg := testImageBytes(t, "jpeg")

	t.Run("returns the final image with its own MIME type", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusOK, map[string]any{
				"candidates": []any{map[string]any{
					"finishReason": "STOP",
					"content": map[string]any{"parts": []any{
						map[string]any{"thought": true, "inlineData": map[string]any{"mimeType": "image/png", "data": "ZHJhZnQ="}},
						map[string]any{"inlineData": map[string]any{"mimeType": "image/jpeg", "data": base64.StdEncoding.EncodeToString(jpg)}},
					}},
				}},
			})
		}
		g := NewGeminiImageGenerator(f.srv.Client(), f.srv.URL+"/v1beta", nil)

		img, err := g.GenerateImage(context.Background(), " a fox ", ImageQualityHigh, ImageAspectRatioLandscape)
		require.NoError(t, err)
		require.Equal(t, "image/jpeg", img.MIMEType, "Gemini answers with JPEG; the type must not be assumed")
		require.Equal(t, base64.StdEncoding.EncodeToString(jpg), img.Base64, "thought (draft) images are skipped")

		require.Equal(t, "/v1beta/models/"+GeminiImageModel+":generateContent", f.lastPath)
		cfg := f.lastBody["generationConfig"].(map[string]any)
		require.Equal(t, []any{"IMAGE"}, cfg["responseModalities"])
		require.Equal(t, map[string]any{"aspectRatio": "3:2", "imageSize": "2K"}, cfg["imageConfig"])
	})

	t.Run("an image-safety finish is a safety violation", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusOK, map[string]any{"candidates": []any{map[string]any{"finishReason": "IMAGE_SAFETY", "content": map[string]any{"parts": []any{}}}}})
		}
		_, err := NewGeminiImageGenerator(f.srv.Client(), f.srv.URL, nil).GenerateImage(context.Background(), "x", ImageQualityLow, ImageAspectRatioSquare)
		requireSafetyViolation(t, err, models.SafetyViolationProviderGoogle, "IMAGE_SAFETY")
	})

	t.Run("a blocked prompt is a safety violation", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusOK, map[string]any{"promptFeedback": map[string]any{"blockReason": "PROHIBITED_CONTENT"}})
		}
		_, err := NewGeminiImageGenerator(f.srv.Client(), f.srv.URL, nil).GenerateImage(context.Background(), "x", ImageQualityLow, ImageAspectRatioSquare)
		requireSafetyViolation(t, err, models.SafetyViolationProviderGoogle, "PROHIBITED_CONTENT")
	})

	t.Run("text with no image is an ordinary error", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusOK, map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": "I can't draw that"}}}}}})
		}
		_, err := NewGeminiImageGenerator(f.srv.Client(), f.srv.URL, nil).GenerateImage(context.Background(), "x", ImageQualityLow, ImageAspectRatioSquare)
		require.ErrorContains(t, err, "I can't draw that")
		var sve *SafetyViolationError
		require.False(t, errors.As(err, &sve))
	})
}

func TestBuildGeminiImageRequest_Shapes(t *testing.T) {
	for aspect, want := range map[ImageAspectRatio]string{ImageAspectRatioSquare: "1:1", ImageAspectRatioPortrait: "2:3", "": "1:1"} {
		req := buildGeminiImageRequest("p", ImageQualityMedium, aspect)
		require.Equal(t, want, req.GenerationConfig.ImageConfig.AspectRatio)
		require.Equal(t, "1K", req.GenerationConfig.ImageConfig.ImageSize)
	}
}

func TestZAIImageGenerator(t *testing.T) {
	pngBytes := testImageBytes(t, "png")

	t.Run("generates, then downloads the result URL", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.file = pngBytes
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusOK, map[string]any{"data": []any{map[string]any{"url": f.srv.URL + "/files/out.png"}}})
		}
		g := NewZAIImageGenerator(f.srv.Client(), f.srv.URL+"/api/paas/v4", nil)

		img, err := g.GenerateImage(context.Background(), "a fox", ImageQualityLow, ImageAspectRatioPortrait)
		require.NoError(t, err)
		require.Equal(t, "image/png", img.MIMEType)
		require.Equal(t, base64.StdEncoding.EncodeToString(pngBytes), img.Base64)
		require.Equal(t, "/api/paas/v4/images/generations", f.lastPath)
		require.Equal(t, map[string]any{"model": ZAIImageModel, "prompt": "a fox", "size": "1056x1568", "quality": "standard"}, f.lastBody)
	})

	t.Run("a content-filter refusal is a safety violation", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "1301", "message": "unsafe content"}})
		}
		_, err := NewZAIImageGenerator(f.srv.Client(), f.srv.URL, nil).GenerateImage(context.Background(), "x", ImageQualityLow, ImageAspectRatioSquare)
		requireSafetyViolation(t, err, models.SafetyViolationProviderZAI, "1301")
	})

	t.Run("other API errors pass through", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "1000", "message": "bad key"}})
		}
		_, err := NewZAIImageGenerator(f.srv.Client(), f.srv.URL, nil).GenerateImage(context.Background(), "x", ImageQualityLow, ImageAspectRatioSquare)
		var apiErr *imageAPIError
		require.True(t, errors.As(err, &apiErr))
		require.Equal(t, http.StatusUnauthorized, apiErr.Status)
	})

	t.Run("high quality asks for hd", func(t *testing.T) {
		require.Equal(t, "hd", buildZAIImageRequest(ZAIImageModel, "p", ImageQualityHigh, ImageAspectRatioSquare).Quality)
		require.Equal(t, "1568x1056", buildZAIImageRequest(ZAIImageModel, "p", ImageQualityMedium, ImageAspectRatioLandscape).Size)
	})
}

func TestQwenImageGenerator(t *testing.T) {
	pngBytes := testImageBytes(t, "png")

	t.Run("generates, then downloads the result URL", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.file = pngBytes
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusOK, map[string]any{"output": map[string]any{"choices": []any{map[string]any{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": []any{map[string]any{"image": f.srv.URL + "/files/out.png"}}},
			}}}})
		}
		g := NewQwenImageGenerator(f.srv.Client(), f.srv.URL+"/api/v1", nil)

		img, err := g.GenerateImage(context.Background(), "a fox", ImageQualityMedium, ImageAspectRatioLandscape)
		require.NoError(t, err)
		require.Equal(t, base64.StdEncoding.EncodeToString(pngBytes), img.Base64)
		require.Equal(t, "/api/v1/services/aigc/multimodal-generation/generation", f.lastPath)
		require.Equal(t, QwenImageModel, f.lastBody["model"])
		require.Equal(t, map[string]any{"size": "1536*1024", "n": float64(1), "watermark": false, "prompt_extend": false}, f.lastBody["parameters"])
		msgs := f.lastBody["input"].(map[string]any)["messages"].([]any)
		require.Equal(t, []any{map[string]any{"text": "a fox"}}, msgs[0].(map[string]any)["content"])
	})

	t.Run("a data-inspection refusal is a safety violation", func(t *testing.T) {
		f := newFakeImageVendor(t)
		f.respond = func(w http.ResponseWriter) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "DataInspectionFailed", "message": "Input data may contain inappropriate content."})
		}
		_, err := NewQwenImageGenerator(f.srv.Client(), f.srv.URL, nil).GenerateImage(context.Background(), "x", ImageQualityLow, ImageAspectRatioSquare)
		requireSafetyViolation(t, err, models.SafetyViolationProviderQwen, "DataInspectionFailed")
	})

	t.Run("high quality stays within the 2048x2048 pixel ceiling", func(t *testing.T) {
		for aspect, want := range map[ImageAspectRatio]string{ImageAspectRatioSquare: "2048*2048", ImageAspectRatioLandscape: "2048*1365", ImageAspectRatioPortrait: "1365*2048"} {
			require.Equal(t, want, buildQwenImageRequest(QwenImageModel, "p", ImageQualityHigh, aspect).Parameters.Size)
		}
	})
}

func TestImageDownloadRefusesNonHTTPSURLs(t *testing.T) {
	for _, u := range []string{"http://169.254.169.254/latest/meta-data", "file:///etc/passwd", "", "https://"} {
		_, err := imageHTTPClient{}.download(context.Background(), u)
		require.ErrorContains(t, err, "non-https", u)
	}
}

func TestImageDownloadRejectsNonImages(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>expired</html>"))
	}))
	defer srv.Close()
	_, err := imageHTTPClient{client: srv.Client()}.download(context.Background(), srv.URL)
	require.ErrorContains(t, err, "not an image")
}

func TestImagePostJSONRetriesRateLimits(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"code": "Throttling.RateQuota"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer srv.Close()

	var out map[string]any
	require.NoError(t, imageHTTPClient{client: srv.Client(), retryDelays: []time.Duration{time.Millisecond, time.Millisecond}}.postJSON(context.Background(), srv.URL, nil, map[string]any{}, &out))
	require.EqualValues(t, 3, calls.Load())
	require.Equal(t, true, out["ok"])
}

func TestImagePostJSONGivesUpAfterTheLastRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"code": "Throttling.RateQuota"})
	}))
	defer srv.Close()

	err := imageHTTPClient{client: srv.Client(), retryDelays: []time.Duration{time.Millisecond}}.postJSON(context.Background(), srv.URL, nil, map[string]any{}, &map[string]any{})
	var apiErr *imageAPIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, http.StatusTooManyRequests, apiErr.Status)
	require.EqualValues(t, 2, calls.Load())
}

func TestImagePostJSONDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "InvalidParameter"})
	}))
	defer srv.Close()

	err := imageHTTPClient{client: srv.Client()}.postJSON(context.Background(), srv.URL, nil, map[string]any{}, &map[string]any{})
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load())
}

func TestImagePostJSONSurfacesCancellationDuringABackoff(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"code": "Throttling.RateQuota"})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := imageHTTPClient{client: srv.Client(), retryDelays: []time.Duration{time.Minute}}.postJSON(ctx, srv.URL, nil, map[string]any{}, &map[string]any{})
	require.ErrorIs(t, err, context.DeadlineExceeded, "the caller learns it was cancelled, not throttled")
	var apiErr *imageAPIError
	require.True(t, errors.As(err, &apiErr), "the throttling that preceded it is kept for logs")
}

func TestImageDownloadWaitsForAResultThatIsNotThereYet(t *testing.T) {
	pngBytes := testImageBytes(t, "png")
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(pngBytes)
	}))
	defer srv.Close()

	img, err := imageHTTPClient{client: srv.Client(), retryDelays: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}}.download(context.Background(), srv.URL+"/out.png")
	require.NoError(t, err)
	require.Equal(t, "image/png", img.MIMEType)
	require.EqualValues(t, 3, calls.Load())
}

func TestImageDownloadGivesUpOnAPermanentFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := imageHTTPClient{client: srv.Client(), retryDelays: []time.Duration{time.Millisecond}}.download(context.Background(), srv.URL)
	require.ErrorContains(t, err, "status 403")
	require.EqualValues(t, 1, calls.Load(), "a 403 is not retried")
}
