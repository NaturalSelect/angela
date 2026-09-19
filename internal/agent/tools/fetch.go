package tools

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/PuerkitoBio/goquery"
)

const (
	MaxFetchSize = 100 * 1024 // 100KB
)

//go:embed fetch.md.tpl
var fetchDescriptionTmpl []byte

var fetchDescriptionTpl = template.Must(
	template.New("fetchDescription").
		Parse(string(fetchDescriptionTmpl)),
)

type fetchDescriptionData struct {
	GhAvailable    bool
	MaxFetchSizeKB int
}

func fetchDescription() string {
	return renderTemplate(fetchDescriptionTpl, fetchDescriptionData{
		GhAvailable:    ghAvailable,
		MaxFetchSizeKB: MaxFetchSize / 1024,
	})
}

func NewFetchTool(workingDir string, client *http.Client) fantasy.AgentTool {
	if client == nil {
		client = newDefaultHTTPClient(defaultToolHTTPTimeout)
	}

	return NewParallelTool(
		toolnames.Fetch,
		fetchDescription(),
		func(ctx context.Context, params FetchParams, call fantasy.ToolCall) Result {
			if params.URL == "" {
				return Fail("URL parameter is required")
			}

			format := strings.ToLower(params.Format)
			if format != "text" && format != "markdown" && format != "html" {
				return Fail("Format must be one of: text, markdown, html")
			}

			if !strings.HasPrefix(params.URL, "http://") && !strings.HasPrefix(params.URL, "https://") {
				return Fail("URL must start with http:// or https://")
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for creating a new file")
			}

			// maxFetchTimeoutSeconds is the maximum allowed timeout for fetch requests (2 minutes)
			const maxFetchTimeoutSeconds = 120

			// Handle timeout with context
			requestCtx := ctx
			if params.Timeout > 0 {
				if params.Timeout > maxFetchTimeoutSeconds {
					params.Timeout = maxFetchTimeoutSeconds
				}
				var cancel context.CancelFunc
				requestCtx, cancel = context.WithTimeout(ctx, time.Duration(params.Timeout)*time.Second)
				defer cancel()
			}

			req, err := http.NewRequestWithContext(requestCtx, "GET", params.URL, nil)
			if err != nil {
				return FailErr("failed to create request", err)
			}

			req.Header.Set("User-Agent", "angela/1.0")

			resp, err := client.Do(req)
			if err != nil {
				return FailErr("failed to fetch URL", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return Failf("Request failed with status code: %d", resp.StatusCode)
			}

			body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchSize))
			if err != nil {
				return Fail("Failed to read response body: " + err.Error())
			}

			content := string(body)

			validUTF8 := utf8.ValidString(content)
			if !validUTF8 {
				return Fail("Response content is not valid UTF-8")
			}
			contentType := resp.Header.Get("Content-Type")

			switch format {
			case "text":
				if strings.Contains(contentType, "text/html") {
					text, err := extractTextFromHTML(content)
					if err != nil {
						return Fail("Failed to extract text from HTML: " + err.Error())
					}
					content = text
				}

			case "markdown":
				if strings.Contains(contentType, "text/html") {
					markdown, err := convertHTMLToMarkdown(content)
					if err != nil {
						return Fail("Failed to convert HTML to Markdown: " + err.Error())
					}
					content = markdown
				}

				content = "```\n" + content + "\n```"

			case "html":
				// return only the body of the HTML document
				if strings.Contains(contentType, "text/html") {
					doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
					if err != nil {
						return Fail("Failed to parse HTML: " + err.Error())
					}
					body, err := doc.Find("body").Html()
					if err != nil {
						return Fail("Failed to extract body from HTML: " + err.Error())
					}
					if body == "" {
						return Fail("No body content found in HTML")
					}
					content = "<html>\n<body>\n" + body + "\n</body>\n</html>"
				}
			}
			// truncate content if it exceeds max read size
			if int64(len(content)) >= MaxFetchSize {
				content = content[:MaxFetchSize]
				content += fmt.Sprintf("\n\n[Content truncated to %d bytes]", MaxFetchSize)
			}

			return Ok(content)
		},
	)
}

func extractTextFromHTML(html string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}

	text := doc.Find("body").Text()
	text = strings.Join(strings.Fields(text), " ")

	return text, nil
}

func convertHTMLToMarkdown(html string) (string, error) {
	converter := md.NewConverter("", true, nil)

	markdown, err := converter.ConvertString(html)
	if err != nil {
		return "", err
	}

	return markdown, nil
}
