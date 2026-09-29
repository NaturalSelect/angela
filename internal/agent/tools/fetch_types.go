package tools

// LargeContentThreshold is the size threshold for saving content to a file.
const LargeContentThreshold = 50000 // 50KB

// WebFetchParams defines the parameters for the web_fetch tool.
type WebFetchParams struct {
	URL string `json:"url" description:"The URL to fetch content from"`
}

// WebFetchPermissionsParams defines the permission parameters for the web_fetch tool.
type WebFetchPermissionsParams struct {
	URL string `json:"url"`
}

// WebSearchParams defines the parameters for the web_search tool.
type WebSearchParams struct {
	Query      string `json:"query" description:"The search query to find information on the web"`
	MaxResults int    `json:"max_results,omitempty" description:"Maximum number of results to return (default: 10, max: 20)"`
	TimeRange  string `json:"time_range,omitempty" description:"Optional recency filter: day, week, month, year, a relative offset like 3d/2w/2mo/1y, or an absolute date YYYY-MM-DD"`
}

// WebSearchPermissionsParams defines the permission parameters for the web_search tool.
type WebSearchPermissionsParams struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
	TimeRange  string `json:"time_range,omitempty"`
}

// MultiSearchParams defines the parameters for the multi_search tool.
type MultiSearchParams struct {
	Query      string   `json:"query" description:"The search query to find information on the web"`
	MaxResults int      `json:"max_results,omitempty" description:"Maximum number of merged results to return (default: 8, max: 20)"`
	Engines    []string `json:"engines,omitempty" description:"Optional list of engine ids to query concurrently (defaults to the top 3 engines in the fallback order)"`
}

// MultiSearchPermissionsParams defines the permission parameters for the
// multi_search tool.
type MultiSearchPermissionsParams struct {
	Query      string   `json:"query"`
	MaxResults int      `json:"max_results,omitempty"`
	Engines    []string `json:"engines,omitempty"`
}

// FetchParams defines the parameters for the simple fetch tool.
type FetchParams struct {
	URL     string `json:"url" description:"The URL to fetch content from"`
	Format  string `json:"format" description:"The format to return the content in (text, markdown, or html)"`
	Timeout int    `json:"timeout,omitempty" description:"Optional timeout in seconds (max 120)"`
}

// FetchPermissionsParams defines the permission parameters for the simple fetch tool.
type FetchPermissionsParams struct {
	URL     string `json:"url"`
	Format  string `json:"format"`
	Timeout int    `json:"timeout,omitempty"`
}
