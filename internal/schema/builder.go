package schema

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/GooferByte/Akto/internal/agent"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Builder converts a slice of ExtractedAPIs into a fully-formed OpenAPI 3.0 spec.
type Builder struct {
	log *zap.Logger
}

// New creates a Builder instance.
func New(log *zap.Logger) *Builder {
	return &Builder{log: log.Named("schema")}
}

// Build converts extracted APIs into an OpenAPI 3.0 specification.
func (b *Builder) Build(apis []*agent.ExtractedAPI, repoURL string) *OpenAPISpec {
	spec := &OpenAPISpec{
		OpenAPI: "3.0.3",
		Info: &Info{
			Title:       "OWASP Juice Shop API",
			Description: fmt.Sprintf("Automatically extracted from %s on %s", repoURL, time.Now().Format("2006-01-02")),
			Version:     "1.0.0",
		},
		Servers: []*Server{
			{URL: "http://localhost:3000", Description: "Local development server"},
		},
		Paths: make(map[string]*PathItem),
		Components: &Components{
			SecuritySchemes: map[string]any{
				"BearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
				},
			},
		},
	}

	tagSet := make(map[string]bool)

	for _, api := range apis {
		if api == nil || api.Method == "" || api.Path == "" {
			continue
		}

		method := strings.ToLower(strings.TrimSpace(api.Method))
		path := normalisePath(api.Path)

		operation := b.buildOperation(api)

		pathItem, exists := spec.Paths[path]
		if !exists {
			pathItem = &PathItem{}
			spec.Paths[path] = pathItem
		}

		if err := setOperation(pathItem, method, operation); err != nil {
			b.log.Warn("skipping duplicate or unsupported method",
				zap.String("method", method),
				zap.String("path", path),
				zap.Error(err),
			)
			continue
		}

		for _, tag := range api.Tags {
			tagSet[tag] = true
		}
	}

	// Populate sorted top-level tags
	for tag := range tagSet {
		spec.Tags = append(spec.Tags, &Tag{Name: tag})
	}
	sort.Slice(spec.Tags, func(i, j int) bool { return spec.Tags[i].Name < spec.Tags[j].Name })

	b.log.Info("OpenAPI spec built",
		zap.Int("paths", len(spec.Paths)),
		zap.Int("tags", len(spec.Tags)),
	)
	return spec
}

// buildOperation converts a single ExtractedAPI into an OpenAPI Operation.
func (b *Builder) buildOperation(api *agent.ExtractedAPI) *Operation {
	op := &Operation{
		Summary:     api.Description,
		Description: api.Description,
		OperationID: operationID(api.Method, api.Path),
		Tags:        api.Tags,
		Responses:   make(map[string]*Response),
	}

	// Security
	if api.RequiresAuth {
		op.Security = []map[string][]string{{"BearerAuth": {}}}
	}

	// Path parameters
	for _, pp := range api.PathParams {
		op.Parameters = append(op.Parameters, &Parameter{
			Name:        pp.Name,
			In:          "path",
			Description: pp.Description,
			Required:    true,
			Schema:      paramSchema(pp.Type),
		})
	}

	// Query parameters
	for _, qp := range api.QueryParams {
		op.Parameters = append(op.Parameters, &Parameter{
			Name:        qp.Name,
			In:          "query",
			Description: qp.Description,
			Required:    qp.Required,
			Schema:      paramSchema(qp.Type),
		})
	}

	// Request body
	if len(api.RequestBody) > 0 {
		contentType := api.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		op.RequestBody = &RequestBody{
			Required: true,
			Content: map[string]*MediaType{
				contentType: {Schema: api.RequestBody},
			},
		}
	}

	// Responses
	successCode := successStatus(api.Method)
	successResp := &Response{Description: "Success"}
	if len(api.Response) > 0 {
		successResp.Content = map[string]*MediaType{
			"application/json": {Schema: api.Response},
		}
	}
	op.Responses[successCode] = successResp

	// Common error responses
	if api.RequiresAuth {
		op.Responses["401"] = &Response{Description: "Unauthorized — missing or invalid JWT"}
	}
	op.Responses["500"] = &Response{Description: "Internal server error"}

	return op
}

// normalisePath converts Express-style :param to OpenAPI-style {param}.
func normalisePath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, ":") {
			segments[i] = "{" + seg[1:] + "}"
		}
	}
	result := strings.Join(segments, "/")
	if !strings.HasPrefix(result, "/") {
		result = "/" + result
	}
	return result
}

// operationID generates a camelCase operation ID from method and path.
func operationID(method, path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '{' || r == '}'
	})
	var sb strings.Builder
	sb.WriteString(strings.ToLower(method))
	for _, p := range parts {
		if p != "" {
			sb.WriteString(strings.ToUpper(p[:1]))
			sb.WriteString(p[1:])
		}
	}
	return sb.String()
}

// paramSchema returns a minimal JSON Schema for a parameter type string.
func paramSchema(typ string) map[string]any {
	t := strings.ToLower(typ)
	switch t {
	case "integer", "int", "number":
		return map[string]any{"type": "integer"}
	case "boolean", "bool":
		return map[string]any{"type": "boolean"}
	default:
		return map[string]any{"type": "string"}
	}
}

// successStatus returns the appropriate 2xx HTTP status code for a method.
func successStatus(method string) string {
	switch strings.ToUpper(method) {
	case "POST":
		return "201"
	case "DELETE":
		return "204"
	default:
		return "200"
	}
}

// setOperation assigns an operation to the matching method field on a PathItem.
func setOperation(item *PathItem, method string, op *Operation) error {
	switch method {
	case "get":
		item.Get = op
	case "post":
		item.Post = op
	case "put":
		item.Put = op
	case "delete":
		item.Delete = op
	case "patch":
		item.Patch = op
	case "options":
		item.Options = op
	case "head":
		item.Head = op
	default:
		return fmt.Errorf("unsupported HTTP method: %s", method)
	}
	return nil
}

// Module registers the schema package with Uber FX.
var Module = fx.Module("schema",
	fx.Provide(New),
)
