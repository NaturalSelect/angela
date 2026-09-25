// Package imagegen wraps the OpenAI Images API (generate and edit) behind
// a small, provider-agnostic interface. It exists so LLM tools that
// generate or edit images do not need to depend on the openai-go SDK
// directly.
package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// DefaultModel is the image model New uses when an Endpoint does not
// specify one.
const DefaultModel = "gpt-image-1"

// requestTimeout bounds a single Generate or Edit call. Image generation
// is a slow, non-streaming operation that can legitimately run past a
// minute — well beyond the idle-read timeout this repo's shared HTTP
// client helpers apply to streaming provider traffic — so Client uses a
// plain *http.Client (see New) and enforces this generous ceiling itself
// instead.
const requestTimeout = 5 * time.Minute

// maxEditSources is the largest number of source images Edit accepts, per
// the OpenAI Images API's own limit for the GPT image models.
const maxEditSources = 16

// Endpoint describes the OpenAI-compatible image API a Client talks to.
type Endpoint struct {
	// ProviderID identifies the configured provider this endpoint came
	// from (e.g. "openai"). It is informational only; it does not
	// affect request construction.
	ProviderID string
	// Model is the image model to request, e.g. "gpt-image-1" or
	// "dall-e-3". Empty defaults to DefaultModel.
	Model string
	// BaseURL overrides the API host. Empty uses the SDK's built-in
	// default (OpenAI's production API).
	BaseURL string
	// APIKey authenticates requests.
	APIKey string
	// Headers are extra HTTP headers sent with every request, e.g. for
	// gateway routing.
	Headers map[string]string
}

// Source is one input image supplied to Edit.
type Source struct {
	// Name is the filename reported to the API, e.g. "image.png".
	Name string
	// MIMEType is the source image's content type, e.g. "image/png".
	MIMEType string
	// Data is the raw image bytes.
	Data []byte
}

// Request describes one image generation or edit call.
type Request struct {
	// Prompt is the text description of the desired image.
	Prompt string
	// Size is the requested output size, e.g. "1024x1024" or "auto".
	// Empty lets the API pick a default.
	Size string
	// Quality is the requested rendering quality (e.g. "high",
	// "medium", "low", "auto", "standard", "hd" — the set of accepted
	// values depends on the model). Empty lets the API pick a default.
	Quality string
	// Background is the requested background handling: "transparent",
	// "opaque", or "auto". Empty lets the API pick a default.
	Background string
	// Sources holds the input image(s) for Edit. Generate ignores this
	// field; Edit requires between 1 and 16 entries.
	Sources []Source
}

// Output is one generated or edited image.
type Output struct {
	// Data is the raw, decoded image bytes.
	Data []byte
	// MIMEType is Data's content type, e.g. "image/png".
	MIMEType string
	// RevisedPrompt is the model's rewritten prompt, when the API
	// returns one (dall-e-3 only). Empty otherwise.
	RevisedPrompt string
}

// Client generates and edits images through an OpenAI-compatible Images
// API.
type Client interface {
	// Model reports the image model this Client requests.
	Model() string
	// Generate creates a new image from a text prompt.
	Generate(ctx context.Context, r Request) (Output, error)
	// Edit creates a new image from a prompt and one or more source
	// images.
	Edit(ctx context.Context, r Request) (Output, error)
}

// New builds a Client that talks to the OpenAI Images API (or an
// OpenAI-compatible gateway) described by ep.
func New(ep Endpoint) Client {
	model := ep.Model
	if model == "" {
		model = DefaultModel
	}

	opts := []option.RequestOption{option.WithAPIKey(ep.APIKey)}
	if baseURL := config.NormalizeBaseURL(ep.BaseURL, catwalk.TypeOpenAI); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	for k, v := range ep.Headers {
		opts = append(opts, option.WithHeader(k, v))
	}
	// Image generation is a slow, non-streaming call that can easily
	// exceed a minute. Use a plain client rather than this repo's
	// shared idle-timeout HTTP client helpers (internal/log), which cap
	// idle reads at two minutes for streaming provider traffic and
	// would risk aborting a legitimate, still-running request; Generate
	// and Edit instead enforce their own requestTimeout via context.
	opts = append(opts, option.WithHTTPClient(&http.Client{}))

	return &openaiClient{
		sdk:   openai.NewClient(opts...),
		model: model,
	}
}

// openaiClient is the Client implementation backed by openai-go.
type openaiClient struct {
	sdk   openai.Client
	model string
}

func (c *openaiClient) Model() string { return c.model }

func (c *openaiClient) Generate(ctx context.Context, r Request) (Output, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	params := openai.ImageGenerateParams{
		Prompt: r.Prompt,
		Model:  openai.ImageModel(c.model),
		N:      openai.Int(1),
	}
	if r.Size != "" {
		params.Size = openai.ImageGenerateParamsSize(r.Size)
	}
	if r.Quality != "" {
		params.Quality = openai.ImageGenerateParamsQuality(r.Quality)
	}
	if r.Background != "" {
		params.Background = openai.ImageGenerateParamsBackground(r.Background)
	}
	if isDallE(c.model) {
		// GPT image models always return base64 and reject
		// response_format if it is set at all; only dall-e-* models
		// accept it, and need it to get base64 instead of a URL.
		params.ResponseFormat = openai.ImageGenerateParamsResponseFormatB64JSON
	}

	resp, err := c.sdk.Images.Generate(ctx, params)
	if err != nil {
		return Output{}, fmt.Errorf("imagegen: generate image: %w", err)
	}
	return outputFromResponse(resp)
}

func (c *openaiClient) Edit(ctx context.Context, r Request) (Output, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	image, err := imageUnionFromSources(r.Sources)
	if err != nil {
		return Output{}, err
	}

	params := openai.ImageEditParams{
		Prompt: r.Prompt,
		Model:  openai.ImageModel(c.model),
		N:      openai.Int(1),
		Image:  image,
	}
	if r.Size != "" {
		params.Size = openai.ImageEditParamsSize(r.Size)
	}
	if r.Quality != "" {
		params.Quality = openai.ImageEditParamsQuality(r.Quality)
	}
	if r.Background != "" {
		params.Background = openai.ImageEditParamsBackground(r.Background)
	}
	if isDallE(c.model) {
		params.ResponseFormat = openai.ImageEditParamsResponseFormatB64JSON
	}

	resp, err := c.sdk.Images.Edit(ctx, params)
	if err != nil {
		return Output{}, fmt.Errorf("imagegen: edit image: %w", err)
	}
	return outputFromResponse(resp)
}

// isDallE reports whether model is one of the legacy dall-e-* models,
// the only ones that accept — and, to get base64 output instead of a
// URL, require — an explicit response_format. GPT image models always
// return base64 and reject the field outright if it is set.
func isDallE(model string) bool {
	return strings.HasPrefix(model, "dall-e")
}

// imageUnionFromSources builds the multipart "image" field for an edit
// request: a single file for one source, or a file array for several, as
// ImageEditParamsImageUnion requires exactly one of its fields set.
func imageUnionFromSources(sources []Source) (openai.ImageEditParamsImageUnion, error) {
	switch {
	case len(sources) == 0:
		return openai.ImageEditParamsImageUnion{}, errors.New("imagegen: edit requires at least one source image")
	case len(sources) > maxEditSources:
		return openai.ImageEditParamsImageUnion{}, fmt.Errorf("imagegen: edit supports at most %d source images, got %d", maxEditSources, len(sources))
	case len(sources) == 1:
		return openai.ImageEditParamsImageUnion{OfFile: uploadFile(sources[0])}, nil
	default:
		files := make([]io.Reader, len(sources))
		for i, src := range sources {
			files[i] = uploadFile(src)
		}
		return openai.ImageEditParamsImageUnion{OfFileArray: files}, nil
	}
}

// uploadFile wraps src for multipart upload, carrying its filename and
// content type through to the request the way openai.File expects.
func uploadFile(src Source) io.Reader {
	return openai.File(bytes.NewReader(src.Data), src.Name, src.MIMEType)
}

// outputFromResponse decodes the first generated image out of resp,
// returning a descriptive error instead of panicking when the response
// is missing the data this package needs.
func outputFromResponse(resp *openai.ImagesResponse) (Output, error) {
	if resp == nil || len(resp.Data) == 0 {
		return Output{}, errors.New("imagegen: response contained no image data")
	}
	img := resp.Data[0]
	if img.B64JSON == "" {
		return Output{}, errors.New("imagegen: response image had no base64 data")
	}
	data, err := base64.StdEncoding.DecodeString(img.B64JSON)
	if err != nil {
		return Output{}, fmt.Errorf("imagegen: decode base64 image data: %w", err)
	}

	mimeType := mimeForOutputFormat(string(resp.OutputFormat))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	return Output{
		Data:          data,
		MIMEType:      mimeType,
		RevisedPrompt: img.RevisedPrompt,
	}, nil
}

// mimeForOutputFormat maps the Images API's output_format value to a
// MIME type. It returns "" for an empty or unrecognized format, so the
// caller can fall back to content sniffing.
func mimeForOutputFormat(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}
