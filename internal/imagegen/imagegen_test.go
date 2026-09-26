package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testEndpoint returns an Endpoint pointed at srv with a fixed API key,
// ready to pass to New.
func testEndpoint(model string, srv *httptest.Server, headers map[string]string) Endpoint {
	return Endpoint{
		ProviderID: "openai",
		Model:      model,
		BaseURL:    srv.URL,
		APIKey:     "test-api-key",
		Headers:    headers,
	}
}

// writeJSON writes v to w as a JSON body, mimicking the Images API.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

// writeImageResponse writes a minimal, well-formed ImagesResponse body
// carrying a single image.
func writeImageResponse(t *testing.T, w http.ResponseWriter, outputFormat, b64 string) {
	t.Helper()
	writeJSON(t, w, map[string]any{
		"created": 1,
		"data": []map[string]any{
			{"b64_json": b64, "revised_prompt": "a revised prompt"},
		},
		"output_format": outputFormat,
	})
}

func TestNew_DefaultsModelWhenEmpty(t *testing.T) {
	t.Parallel()

	client := New(Endpoint{APIKey: "key"})
	require.Equal(t, DefaultModel, client.Model())
}

func TestNew_KeepsExplicitModel(t *testing.T) {
	t.Parallel()

	client := New(Endpoint{APIKey: "key", Model: "dall-e-3"})
	require.Equal(t, "dall-e-3", client.Model())
}

func TestNew_DefaultsTimeoutWhenUnset(t *testing.T) {
	t.Parallel()

	client := New(Endpoint{APIKey: "key"})
	oc, ok := client.(*openaiClient)
	require.True(t, ok)
	require.Equal(t, DefaultRequestTimeout, oc.timeout)
}

func TestNew_KeepsExplicitTimeout(t *testing.T) {
	t.Parallel()

	client := New(Endpoint{APIKey: "key", Timeout: 90 * time.Second})
	oc, ok := client.(*openaiClient)
	require.True(t, ok)
	require.Equal(t, 90*time.Second, oc.timeout)
}

func TestGenerate_HitsGenerationsEndpointWithAuthHeadersAndBody(t *testing.T) {
	t.Parallel()

	wantImage := []byte("hello generated image bytes \x00\x01\xff")
	b64 := base64.StdEncoding.EncodeToString(wantImage)

	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotCustom string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Custom-Header")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotBody))

		writeImageResponse(t, w, "png", b64)
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, map[string]string{"X-Custom-Header": "custom-value"}))
	out, err := client.Generate(t.Context(), Request{
		Prompt:     "a cat wearing a hat",
		Size:       "1024x1024",
		Quality:    "high",
		Background: "opaque",
	})
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/images/generations", gotPath)
	require.Equal(t, "Bearer test-api-key", gotAuth)
	require.Equal(t, "custom-value", gotCustom)

	require.Equal(t, "a cat wearing a hat", gotBody["prompt"])
	require.Equal(t, "gpt-image-1", gotBody["model"])
	require.Equal(t, "1024x1024", gotBody["size"])
	require.Equal(t, "high", gotBody["quality"])
	require.Equal(t, "opaque", gotBody["background"])
	require.EqualValues(t, 1, gotBody["n"])
	require.NotContains(t, gotBody, "response_format")

	require.Equal(t, wantImage, out.Data)
	require.Equal(t, "image/png", out.MIMEType)
	require.Equal(t, "a revised prompt", out.RevisedPrompt)
}

func TestGenerate_ResponseFormatOnlySetForDallE(t *testing.T) {
	t.Parallel()
	tests := []struct {
		model       string
		wantPresent bool
	}{
		{"dall-e-2", true},
		{"dall-e-3", true},
		{"gpt-image-1", false},
		{"gpt-image-1-mini", false},
		{"gpt-image-1.5", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()

			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(body, &gotBody))
				writeImageResponse(t, w, "png", base64.StdEncoding.EncodeToString([]byte("x")))
			}))
			defer srv.Close()

			client := New(testEndpoint(tt.model, srv, nil))
			_, err := client.Generate(t.Context(), Request{Prompt: "x"})
			require.NoError(t, err)

			if tt.wantPresent {
				require.Equal(t, "b64_json", gotBody["response_format"])
			} else {
				require.NotContains(t, gotBody, "response_format")
			}
		})
	}
}

func TestGenerate_MIMETypeFallsBackToSniffingWhenOutputFormatMissing(t *testing.T) {
	t.Parallel()

	// PNG magic bytes, enough for http.DetectContentType to recognize.
	pngMagic := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	b64 := base64.StdEncoding.EncodeToString(pngMagic)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"created": 1,
			"data":    []map[string]any{{"b64_json": b64}},
		})
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, nil))
	out, err := client.Generate(t.Context(), Request{Prompt: "x"})
	require.NoError(t, err)
	require.Equal(t, "image/png", out.MIMEType)
}

func TestGenerate_EmptyDataProducesError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"created": 1, "data": []map[string]any{}})
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, nil))
	_, err := client.Generate(t.Context(), Request{Prompt: "x"})
	require.Error(t, err)
}

func TestGenerate_EmptyB64JSONProducesError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"created": 1,
			"data":    []map[string]any{{"b64_json": ""}},
		})
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, nil))
	_, err := client.Generate(t.Context(), Request{Prompt: "x"})
	require.Error(t, err)
}

func TestGenerate_InvalidBase64ProducesError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"created": 1,
			"data":    []map[string]any{{"b64_json": "not-valid-base64!!"}},
		})
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, nil))
	_, err := client.Generate(t.Context(), Request{Prompt: "x"})
	require.Error(t, err)
}

func TestGenerate_RespectsCanceledContext(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeImageResponse(t, w, "png", base64.StdEncoding.EncodeToString([]byte("x")))
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, nil))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := client.Generate(ctx, Request{Prompt: "x"})
	require.Error(t, err)
}

func TestEdit_SingleSourceMultipartRequest(t *testing.T) {
	t.Parallel()

	srcBytes := []byte("source image bytes \x00\x01\x02\xff")
	outBytes := []byte("edited image bytes")
	outB64 := base64.StdEncoding.EncodeToString(outBytes)

	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotAuth        string
		gotPrompt      string
		gotModel       string
		gotFileName    string
		gotFileCT      string
		gotFileBytes   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")

		require.NoError(t, r.ParseMultipartForm(10<<20))
		gotPrompt = r.FormValue("prompt")
		gotModel = r.FormValue("model")

		files := r.MultipartForm.File["image"]
		require.Len(t, files, 1)
		fh := files[0]
		gotFileName = fh.Filename
		gotFileCT = fh.Header.Get("Content-Type")

		f, err := fh.Open()
		require.NoError(t, err)
		defer f.Close()
		gotFileBytes, err = io.ReadAll(f)
		require.NoError(t, err)

		writeImageResponse(t, w, "png", outB64)
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, map[string]string{"X-Custom-Header": "v"}))
	out, err := client.Edit(t.Context(), Request{
		Prompt: "add a hat",
		Sources: []Source{
			{Name: "cat.png", MIMEType: "image/png", Data: srcBytes},
		},
	})
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/images/edits", gotPath)
	require.Contains(t, gotContentType, "multipart/form-data")
	require.Equal(t, "Bearer test-api-key", gotAuth)
	require.Equal(t, "add a hat", gotPrompt)
	require.Equal(t, "gpt-image-1", gotModel)
	require.Equal(t, "cat.png", gotFileName)
	require.Equal(t, "image/png", gotFileCT)
	require.Equal(t, srcBytes, gotFileBytes)

	require.Equal(t, outBytes, out.Data)
}

func TestEdit_MultipleSourcesUseFileArrayField(t *testing.T) {
	t.Parallel()

	src1 := []byte("first source image")
	src2 := []byte("second source image")
	outB64 := base64.StdEncoding.EncodeToString([]byte("out"))

	var gotFiles [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))

		// A single source is sent under "image"; multiple sources are
		// sent as repeated "image[]" fields.
		require.Empty(t, r.MultipartForm.File["image"])
		files := r.MultipartForm.File["image[]"]
		require.Len(t, files, 2)
		for _, fh := range files {
			f, err := fh.Open()
			require.NoError(t, err)
			b, err := io.ReadAll(f)
			require.NoError(t, err)
			require.NoError(t, f.Close())
			gotFiles = append(gotFiles, b)
		}

		writeImageResponse(t, w, "png", outB64)
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, nil))
	_, err := client.Edit(t.Context(), Request{
		Prompt: "combine these",
		Sources: []Source{
			{Name: "a.png", MIMEType: "image/png", Data: src1},
			{Name: "b.png", MIMEType: "image/png", Data: src2},
		},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, [][]byte{src1, src2}, gotFiles)
}

func TestEdit_ResponseFormatOnlySetForDallE(t *testing.T) {
	t.Parallel()
	tests := []struct {
		model       string
		wantPresent bool
	}{
		{"dall-e-2", true},
		{"gpt-image-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()

			var gotResponseFormat string
			var sawResponseFormat bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseMultipartForm(10<<20))
				if vs, ok := r.MultipartForm.Value["response_format"]; ok && len(vs) > 0 {
					sawResponseFormat = true
					gotResponseFormat = vs[0]
				}
				writeImageResponse(t, w, "png", base64.StdEncoding.EncodeToString([]byte("x")))
			}))
			defer srv.Close()

			client := New(testEndpoint(tt.model, srv, nil))
			_, err := client.Edit(t.Context(), Request{
				Prompt:  "x",
				Sources: []Source{{Name: "a.png", MIMEType: "image/png", Data: []byte("a")}},
			})
			require.NoError(t, err)

			require.Equal(t, tt.wantPresent, sawResponseFormat)
			if tt.wantPresent {
				require.Equal(t, "b64_json", gotResponseFormat)
			}
		})
	}
}

func TestEdit_NoSourcesProducesError(t *testing.T) {
	t.Parallel()

	client := New(Endpoint{APIKey: "key", Model: "gpt-image-1"})
	_, err := client.Edit(t.Context(), Request{Prompt: "x"})
	require.Error(t, err)
}

func TestEdit_TooManySourcesProducesError(t *testing.T) {
	t.Parallel()

	sources := make([]Source, maxEditSources+1)
	for i := range sources {
		sources[i] = Source{Name: "a.png", MIMEType: "image/png", Data: []byte("a")}
	}

	client := New(Endpoint{APIKey: "key", Model: "gpt-image-1"})
	_, err := client.Edit(t.Context(), Request{Prompt: "x", Sources: sources})
	require.Error(t, err)
}

func TestEdit_EmptyDataProducesError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"created": 1, "data": []map[string]any{}})
	}))
	defer srv.Close()

	client := New(testEndpoint("gpt-image-1", srv, nil))
	_, err := client.Edit(t.Context(), Request{
		Prompt:  "x",
		Sources: []Source{{Name: "a.png", MIMEType: "image/png", Data: []byte("a")}},
	})
	require.Error(t, err)
}

func TestMimeForOutputFormat(t *testing.T) {
	t.Parallel()

	require.Equal(t, "image/png", mimeForOutputFormat("png"))
	require.Equal(t, "image/jpeg", mimeForOutputFormat("jpeg"))
	require.Equal(t, "image/webp", mimeForOutputFormat("webp"))
	require.Empty(t, mimeForOutputFormat(""))
	require.Empty(t, mimeForOutputFormat("unknown"))
}

func TestIsDallE(t *testing.T) {
	t.Parallel()

	require.True(t, isDallE("dall-e-2"))
	require.True(t, isDallE("dall-e-3"))
	require.False(t, isDallE("gpt-image-1"))
	require.False(t, isDallE("gpt-image-1-mini"))
	require.False(t, isDallE(""))
}
