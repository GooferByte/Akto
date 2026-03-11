package agent

// ExtractedAPI represents a single HTTP endpoint extracted from the repository.
type ExtractedAPI struct {
	Method      string           `json:"method"`
	Path        string           `json:"path"`
	Description string           `json:"description,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	PathParams  []ExtractedParam `json:"path_params,omitempty"`
	QueryParams []ExtractedParam `json:"query_params,omitempty"`
	// RequestBody is a free-form JSON Schema object describing the request body.
	RequestBody map[string]any `json:"request_body,omitempty"`
	// Response is a free-form JSON Schema object describing the primary success response.
	Response     map[string]any `json:"response,omitempty"`
	RequiresAuth bool           `json:"requires_auth"`
	ContentType  string         `json:"content_type,omitempty"`
}

// ExtractedParam represents a path or query parameter.
type ExtractedParam struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Type        string `json:"type,omitempty"` // string, integer, boolean, …
}
