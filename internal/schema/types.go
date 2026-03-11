package schema

// OpenAPISpec is the top-level OpenAPI 3.0 document.
type OpenAPISpec struct {
	OpenAPI    string               `json:"openapi"              yaml:"openapi"`
	Info       *Info                `json:"info"                 yaml:"info"`
	Servers    []*Server            `json:"servers,omitempty"    yaml:"servers,omitempty"`
	Tags       []*Tag               `json:"tags,omitempty"       yaml:"tags,omitempty"`
	Paths      map[string]*PathItem `json:"paths"                yaml:"paths"`
	Components *Components          `json:"components,omitempty" yaml:"components,omitempty"`
}

// Info describes the API.
type Info struct {
	Title       string `json:"title"                 yaml:"title"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Version     string `json:"version"               yaml:"version"`
}

// Server represents a server entry.
type Server struct {
	URL         string `json:"url"                   yaml:"url"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Tag is a grouping label for operations.
type Tag struct {
	Name        string `json:"name"                  yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// PathItem holds the operations for a single path.
type PathItem struct {
	Get     *Operation `json:"get,omitempty"     yaml:"get,omitempty"`
	Post    *Operation `json:"post,omitempty"    yaml:"post,omitempty"`
	Put     *Operation `json:"put,omitempty"     yaml:"put,omitempty"`
	Delete  *Operation `json:"delete,omitempty"  yaml:"delete,omitempty"`
	Patch   *Operation `json:"patch,omitempty"   yaml:"patch,omitempty"`
	Options *Operation `json:"options,omitempty" yaml:"options,omitempty"`
	Head    *Operation `json:"head,omitempty"    yaml:"head,omitempty"`
}

// Operation describes a single HTTP operation on a path.
type Operation struct {
	Summary     string                `json:"summary,omitempty"      yaml:"summary,omitempty"`
	Description string                `json:"description,omitempty"  yaml:"description,omitempty"`
	OperationID string                `json:"operationId,omitempty"  yaml:"operationId,omitempty"`
	Tags        []string              `json:"tags,omitempty"         yaml:"tags,omitempty"`
	Security    []map[string][]string `json:"security,omitempty"     yaml:"security,omitempty"`
	Parameters  []*Parameter          `json:"parameters,omitempty"   yaml:"parameters,omitempty"`
	RequestBody *RequestBody          `json:"requestBody,omitempty"  yaml:"requestBody,omitempty"`
	Responses   map[string]*Response  `json:"responses"              yaml:"responses"`
}

// Parameter represents a query, path, or header parameter.
type Parameter struct {
	Name        string         `json:"name"                  yaml:"name"`
	In          string         `json:"in"                    yaml:"in"` // path | query | header | cookie
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool           `json:"required"              yaml:"required"`
	Schema      map[string]any `json:"schema,omitempty"      yaml:"schema,omitempty"`
}

// RequestBody describes the HTTP request body.
type RequestBody struct {
	Description string                `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool                  `json:"required"              yaml:"required"`
	Content     map[string]*MediaType `json:"content"               yaml:"content"`
}

// MediaType wraps a JSON Schema for a specific content type.
type MediaType struct {
	Schema map[string]any `json:"schema" yaml:"schema"`
}

// Response describes a single response.
type Response struct {
	Description string                `json:"description"           yaml:"description"`
	Content     map[string]*MediaType `json:"content,omitempty"     yaml:"content,omitempty"`
}

// Components holds reusable schema definitions and security schemes.
type Components struct {
	Schemas         map[string]map[string]any `json:"schemas,omitempty"         yaml:"schemas,omitempty"`
	SecuritySchemes map[string]any            `json:"securitySchemes,omitempty" yaml:"securitySchemes,omitempty"`
}
