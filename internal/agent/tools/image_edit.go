package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"charm.land/fantasy"

	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/imagegen"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed image_edit.md
var imageEditDescription string

// maxSourceImages is the largest number of source images ImageEdit
// accepts, matching imagegen.Client.Edit's own limit. Checking it here
// too, before any image_ids lookup or file read, lets a bad count fail
// immediately with a clear message instead of after wasted DB or
// filesystem work.
const maxSourceImages = 16

// maxSourceImageBytes rejects a single source image larger than this
// many bytes, so an oversized file cannot ride an edit request all the
// way to the provider only to be rejected there.
const maxSourceImageBytes = 20 * 1024 * 1024

// allowedSourceMIMETypes are the image content types a source image may
// have, matched against what http.DetectContentType actually finds in
// the bytes rather than a filename or caller-supplied type.
var allowedSourceMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// ImageEditParams are the ImageEdit tool's parameters.
type ImageEditParams struct {
	Prompt     string   `json:"prompt" description:"A text description of how to edit or revise the source image(s)."`
	ImageIDs   []string `json:"image_ids,omitempty" description:"IDs of previously generated or edited images to use as sources, e.g. [\"img_1a2b3c4d5e6f\"]. Combined with file_paths, 1-16 source images total are required."`
	FilePaths  []string `json:"file_paths,omitempty" description:"Paths to image files on disk to use as sources. Combined with image_ids, 1-16 source images total are required."`
	Size       string   `json:"size,omitempty" description:"The requested output size, e.g. \"1024x1024\". Leave unset to let the model choose."`
	Quality    string   `json:"quality,omitempty" description:"The requested rendering quality, e.g. \"high\", \"medium\", \"low\", \"auto\". Leave unset to let the model choose."`
	Background string   `json:"background,omitempty" description:"Background handling: \"transparent\", \"opaque\", or \"auto\". Leave unset to let the model choose."`
}

// ImageEditPermissionsParams mirrors ImageEditParams for the approval
// dialog, without the schema-only description tags.
type ImageEditPermissionsParams struct {
	Prompt     string   `json:"prompt"`
	ImageIDs   []string `json:"image_ids,omitempty"`
	FilePaths  []string `json:"file_paths,omitempty"`
	Size       string   `json:"size,omitempty"`
	Quality    string   `json:"quality,omitempty"`
	Background string   `json:"background,omitempty"`
}

// NewImageEditTool builds the ImageEdit tool. f resolves the client (and
// its credentials) at call time; store both resolves image_ids sources
// and persists the full-size result; workingDir bounds file_paths
// sources the same way it bounds the Read tool.
func NewImageEditTool(f ImageClientFactory, store images.Service, workingDir string) fantasy.AgentTool {
	return NewTool(
		toolnames.ImageEdit,
		imageEditDescription,
		func(ctx context.Context, params ImageEditParams, call fantasy.ToolCall) Result {
			prompt := strings.TrimSpace(params.Prompt)
			if prompt == "" {
				return Fail("prompt is required")
			}

			sources, resolvedIDs, err := resolveEditSources(ctx, store, workingDir, params.ImageIDs, params.FilePaths)
			if err != nil {
				return Fail(err.Error())
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for editing images")
			}

			client, err := f(ctx)
			if err != nil {
				return FailErr("failed to set up the image generation client", err)
			}

			output, err := client.Edit(ctx, imagegen.Request{
				Prompt:     prompt,
				Size:       params.Size,
				Quality:    params.Quality,
				Background: params.Background,
				Sources:    sources,
			})
			if err != nil {
				return FailErr("failed to edit image", err)
			}

			return finishImageResult(ctx, store, imageResultParams{
				sessionID:      sessionID,
				toolCallID:     call.ID,
				prompt:         prompt,
				revisedPrompt:  output.RevisedPrompt,
				model:          client.Model(),
				data:           output.Data,
				mimeType:       output.MIMEType,
				sourceImageIDs: resolvedIDs,
				transparent:    params.Background == backgroundTransparent,
				verb:           "Edited",
			})
		},
	)
}

// resolveEditSources validates and loads every source image an edit call
// asked for: image_ids from store, file_paths from disk. It rejects a
// bad combined count before either lookup begins, so a hopeless call
// never reaches the DB or filesystem. resolvedIDs echoes back exactly
// the image_ids that resolved, for SourceImageIDs on the stored row.
func resolveEditSources(
	ctx context.Context,
	store images.Service,
	workingDir string,
	imageIDs, filePaths []string,
) ([]imagegen.Source, []string, error) {
	total := len(imageIDs) + len(filePaths)
	if total == 0 {
		return nil, nil, errors.New("at least one source image is required: pass image_ids and/or file_paths")
	}
	if total > maxSourceImages {
		return nil, nil, fmt.Errorf("too many source images: got %d, the maximum is %d", total, maxSourceImages)
	}

	sources := make([]imagegen.Source, 0, total)
	resolvedIDs := make([]string, 0, len(imageIDs))

	for _, id := range imageIDs {
		img, err := store.Get(ctx, id)
		if err != nil {
			if errors.Is(err, images.ErrNotFound) {
				return nil, nil, fmt.Errorf("source image not found: %s", id)
			}
			return nil, nil, fmt.Errorf("failed to load source image %s: %w", id, err)
		}
		mimeType, err := validateSourceImage(img.Data, id)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, imagegen.Source{
			Name:     id + extensionForMIME(mimeType),
			MIMEType: mimeType,
			Data:     img.Data,
		})
		resolvedIDs = append(resolvedIDs, id)
	}

	for _, path := range filePaths {
		data, name, err := readSourceImageFile(workingDir, path)
		if err != nil {
			return nil, nil, err
		}
		mimeType, err := validateSourceImage(data, path)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, imagegen.Source{
			Name:     name,
			MIMEType: mimeType,
			Data:     data,
		})
	}

	return sources, resolvedIDs, nil
}

// readSourceImageFile resolves path against workingDir the same way the
// Read tool resolves file_path (filepathext.SmartJoin), then rejects it
// unless it stays within workingDir. Containment reuses isInSkillsPath —
// the Read tool's own symlink-aware "is this path inside that root"
// check, applied here against a single root instead of the configured
// skills directories — rather than a raw string-prefix comparison, which
// a sibling directory sharing workingDir's name as a prefix, or a
// symlink pointing back out, would fool.
func readSourceImageFile(workingDir, path string) ([]byte, string, error) {
	abs, err := filepath.Abs(filepathext.SmartJoin(workingDir, path))
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve source image path %s: %w", path, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, "", fmt.Errorf("source image file not found: %s", path)
	}
	if info.IsDir() {
		return nil, "", fmt.Errorf("source image path is a directory, not a file: %s", path)
	}
	if !isInSkillsPath(abs, []string{workingDir}) {
		return nil, "", fmt.Errorf("source image file path resolves outside the working directory: %s", path)
	}
	if info.Size() > maxSourceImageBytes {
		return nil, "", fmt.Errorf("source image file %s is too large (%d bytes); the maximum is %d bytes", path, info.Size(), maxSourceImageBytes)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read source image file %s: %w", path, err)
	}
	return data, filepath.Base(abs), nil
}

// validateSourceImage checks that data is within the size limit and a
// supported image type detected from its bytes, returning the sniffed
// MIME type for the caller to attach to the imagegen.Source.
func validateSourceImage(data []byte, label string) (string, error) {
	if len(data) > maxSourceImageBytes {
		return "", fmt.Errorf("source image %s is too large (%d bytes); the maximum is %d bytes", label, len(data), maxSourceImageBytes)
	}
	mimeType := detectContentType(data)
	if !allowedSourceMIMETypes[mimeType] {
		return "", fmt.Errorf("source image %s has an unsupported content type %q; only PNG, JPEG, and WEBP are accepted", label, mimeType)
	}
	return mimeType, nil
}

// detectContentType sniffs data's content type from its bytes, the same
// way the Read tool sniffs an image it is about to hand back to the
// model (see sniffImageMimeType), rather than trusting a filename or
// caller-supplied type that could disagree with what is actually there.
func detectContentType(data []byte) string {
	sniffed := http.DetectContentType(data)
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = strings.TrimSpace(sniffed[:i])
	}
	return sniffed
}

// extensionForMIME names the upload filename extension for a source
// image resolved from an image_ids entry, which otherwise has none.
func extensionForMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}
