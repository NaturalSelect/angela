package tools

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	"charm.land/fantasy"
	"github.com/disintegration/imaging"

	"github.com/NaturalSelect/angela/internal/imagegen"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed image_generate.md
var imageGenerateDescription string

// ImageClientFactory builds the imagegen.Client to use for one tool
// call. It is called once per invocation rather than once when the tool
// is constructed: credential resolution can depend on state that is only
// settled at call time, and building the coordinator's tool set must not
// itself require a working provider.
type ImageClientFactory func(ctx context.Context) (imagegen.Client, error)

// imageProvider identifies the backend behind imagegen.Client for
// storage purposes. imagegen currently wraps the OpenAI Images API
// exclusively, and imagegen.Client exposes no separate provider
// identifier of its own to read back.
const imageProvider = "openai"

// backgroundTransparent is the Background request value that asks for an
// alpha channel. A preview built from such an image is worth keeping as
// PNG; anything else is smaller as JPEG.
const backgroundTransparent = "transparent"

// previewMaxDimension bounds the longer side of the inline chat preview.
// The full-size original always goes to store.Create untouched; this
// only shrinks what gets echoed back into the conversation.
const previewMaxDimension = 768

// previewJPEGQuality is the encoding quality used for a non-transparent
// preview.
const previewJPEGQuality = 85

// ImageResponseMetadata is the structured metadata attached to a
// successful ImageGenerate/ImageEdit result. A later TUI step renders
// the response from this rather than re-parsing the caption text, so
// its shape is part of this feature's cross-package contract and must
// not change casually.
type ImageResponseMetadata struct {
	ImageID        string   `json:"image_id"`
	MIMEType       string   `json:"mime_type"`
	Model          string   `json:"model"`
	Prompt         string   `json:"prompt"`
	RevisedPrompt  string   `json:"revised_prompt"`
	Width          int      `json:"width"`
	Height         int      `json:"height"`
	SourceImageIDs []string `json:"source_image_ids,omitempty"`
}

// ImageGenerateParams are the ImageGenerate tool's parameters.
type ImageGenerateParams struct {
	Prompt     string `json:"prompt" description:"A text description of the image to generate."`
	Size       string `json:"size,omitempty" description:"The requested output size, e.g. \"1024x1024\". Leave unset to let the model choose."`
	Quality    string `json:"quality,omitempty" description:"The requested rendering quality, e.g. \"high\", \"medium\", \"low\", \"auto\". Leave unset to let the model choose."`
	Background string `json:"background,omitempty" description:"Background handling: \"transparent\", \"opaque\", or \"auto\". Leave unset to let the model choose."`
}

// ImageGeneratePermissionsParams mirrors ImageGenerateParams for the
// approval dialog, without the schema-only description tags.
type ImageGeneratePermissionsParams struct {
	Prompt     string `json:"prompt"`
	Size       string `json:"size,omitempty"`
	Quality    string `json:"quality,omitempty"`
	Background string `json:"background,omitempty"`
}

// NewImageGenerateTool builds the ImageGenerate tool. f resolves the
// client (and its credentials) at call time; store persists the
// full-size result so it can be exported or edited later.
func NewImageGenerateTool(f ImageClientFactory, store images.Service) fantasy.AgentTool {
	return NewTool(
		toolnames.ImageGenerate,
		imageGenerateDescription,
		func(ctx context.Context, params ImageGenerateParams, call fantasy.ToolCall) Result {
			prompt := strings.TrimSpace(params.Prompt)
			if prompt == "" {
				return Fail("prompt is required")
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for generating images")
			}

			client, err := f(ctx)
			if err != nil {
				return FailErr("failed to set up the image generation client", err)
			}

			output, err := client.Generate(ctx, imagegen.Request{
				Prompt:     prompt,
				Size:       params.Size,
				Quality:    params.Quality,
				Background: params.Background,
			})
			if err != nil {
				return FailErr("failed to generate image", err)
			}

			return finishImageResult(ctx, store, imageResultParams{
				sessionID:     sessionID,
				toolCallID:    call.ID,
				prompt:        prompt,
				revisedPrompt: output.RevisedPrompt,
				model:         client.Model(),
				data:          output.Data,
				mimeType:      output.MIMEType,
				transparent:   params.Background == backgroundTransparent,
				verb:          "Generated",
			})
		},
	)
}

// imageResultParams carries what finishImageResult needs to persist and
// describe a newly produced image. It is shared by ImageGenerate and
// ImageEdit so the decode/store/preview/caption tail is written once.
type imageResultParams struct {
	sessionID      string
	toolCallID     string
	prompt         string
	revisedPrompt  string
	model          string
	data           []byte
	mimeType       string
	sourceImageIDs []string
	transparent    bool
	// verb opens the caption, e.g. "Generated" or "Edited".
	verb string
}

// finishImageResult is the tail shared by ImageGenerate and ImageEdit
// once the provider has returned an image: measure it, persist the
// full-size original, and hand the model back a small preview plus the
// id it needs to refer to this image again.
func finishImageResult(ctx context.Context, store images.Service, p imageResultParams) Result {
	img, _, decodeErr := image.Decode(bytes.NewReader(p.data))
	var width, height int
	if decodeErr == nil && img != nil {
		bounds := img.Bounds()
		width, height = bounds.Dx(), bounds.Dy()
	}

	created, err := store.Create(ctx, images.CreateParams{
		SessionID:      p.sessionID,
		ToolCallID:     p.toolCallID,
		Prompt:         p.prompt,
		RevisedPrompt:  p.revisedPrompt,
		SourceImageIDs: p.sourceImageIDs,
		Provider:       imageProvider,
		Model:          p.model,
		MIMEType:       p.mimeType,
		Width:          width,
		Height:         height,
		Data:           p.data,
	})
	if err != nil {
		return FailErr("failed to save generated image", err)
	}

	previewData, previewMIME := buildPreview(img, p.data, p.mimeType, p.transparent)

	caption := fmt.Sprintf(
		"%s image %s (%dx%d, %s). To modify it, call %s with image_ids [%q]. "+
			`The user can save the full-size image with the "Export Image" command.`,
		p.verb, created.ID, width, height, p.model, toolnames.ImageEdit, created.ID,
	)

	meta := ImageResponseMetadata{
		ImageID:        created.ID,
		MIMEType:       created.MIMEType,
		Model:          created.Model,
		Prompt:         created.Prompt,
		RevisedPrompt:  created.RevisedPrompt,
		Width:          created.Width,
		Height:         created.Height,
		SourceImageIDs: created.SourceImageIDs,
	}

	return ImageWithText(previewData, previewMIME, caption).WithMetadata(meta)
}

// buildPreview downsizes img to fit the chat preview footprint; the
// full-size original already went to store.Create and stays retrievable
// through the "Export Image" command, so sending it back as the tool
// result too would bloat every turn with a payload nobody asked to see
// inline. img is nil when decoding the provider's bytes failed, in which
// case the original bytes stand in as the preview rather than failing
// the tool call over a cosmetic step.
func buildPreview(img image.Image, original []byte, originalMIME string, transparent bool) ([]byte, string) {
	if img == nil {
		return original, originalMIME
	}

	resized := imaging.Fit(img, previewMaxDimension, previewMaxDimension, imaging.Lanczos)

	var buf bytes.Buffer
	if transparent {
		if err := png.Encode(&buf, resized); err != nil {
			return original, originalMIME
		}
		return buf.Bytes(), "image/png"
	}

	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: previewJPEGQuality}); err != nil {
		return original, originalMIME
	}
	return buf.Bytes(), "image/jpeg"
}
