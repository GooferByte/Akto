package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/GooferByte/Akto/internal/config"
	"github.com/google/generative-ai-go/genai"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/api/option"
)

const (
	maxIterations = 120 // hard cap on agent loop iterations to prevent infinite runs

	systemPrompt = `You are an expert API extraction agent specialised in Node.js / Express.js applications.

## YOUR GOAL
Systematically analyse the provided repository and extract EVERY REST API endpoint with its complete request / response schema.

## STRATEGY
1. Call list_directory(".") to understand the root project layout.
2. Find and read the main entry point (server.ts / server.js / app.ts / app.js / index.ts / index.js).
3. Identify all app.use() / router.use() calls that mount sub-routers with base paths.
4. Explore the routes/ directory (and any other controller / handler directories).
5. For each route file, read it with read_file and extract all HTTP endpoints.
6. Use search_code to find patterns you may have missed (e.g. "router.get", "router.post").
7. Check lib/, models/, and types/ directories for request / response shapes.
8. Once ALL route files are covered, call submit_apis with the complete list.

## EXPRESS.JS PATTERNS TO RECOGNISE
- router.get / post / put / delete / patch / all ("/path", handler)
- app.get / post / put / delete / patch ("/path", handler)
- router.route("/path").get(h).post(h).put(h)
- app.use("/base", subRouter)   ← accumulate base paths
- GraphQL: app.use("/graphql", …)
- File uploads: look for multer usage → multipart/form-data
- Auth middleware: passport, jwt.verify, security middleware → mark requires_auth

## WHAT TO CAPTURE PER ENDPOINT
| Field         | Description                                                        |
|---------------|--------------------------------------------------------------------|
| method        | HTTP verb in UPPER CASE                                            |
| path          | Full path including mounted base (e.g. /api/users/:id)            |
| description   | Plain-English summary inferred from the handler code               |
| tags          | Logical group derived from the resource name (Users, Orders, …)   |
| path_params   | :param segments — name, type, required=true                       |
| query_params  | ?key=val — name, type, required flag, description                 |
| request_body  | JSON Schema object for POST/PUT/PATCH bodies                       |
| response      | JSON Schema object for the primary 2xx response                    |
| requires_auth | true if any auth middleware guards this route                      |
| content_type  | "application/json" | "multipart/form-data" | etc.                |

## SUBMIT FORMAT
When you are finished, call submit_apis with apis_json set to a valid JSON array string:
[
  {
    "method": "GET",
    "path": "/api/items",
    "description": "List all items, supports search",
    "tags": ["Items"],
    "path_params": [],
    "query_params": [{"name":"q","description":"Search term","required":false,"type":"string"}],
    "request_body": null,
    "response": {"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}},
    "requires_auth": false,
    "content_type": "application/json"
  }
]

Be thorough — do NOT call submit_apis until you have covered every route file in the repository.`
)

// Agent runs an autonomous tool-calling loop powered by Gemini to extract API endpoints.
type Agent struct {
	client    *genai.Client
	modelName string
	log       *zap.Logger
}

// AgentParams groups Agent dependencies for Uber FX injection.
type AgentParams struct {
	fx.In

	Config *config.Config
	Log    *zap.Logger
}

// New creates an Agent and registers a lifecycle hook to close the Gemini client on shutdown.
func New(lc fx.Lifecycle, p AgentParams) (*Agent, error) {
	ctx := context.Background()

	client, err := genai.NewClient(ctx, option.WithAPIKey(p.Config.GeminiAPIKey))
	if err != nil {
		return nil, fmt.Errorf("create gemini client: %w", err)
	}

	lc.Append(fx.Hook{
		OnStop: func(_ context.Context) error {
			return client.Close()
		},
	})

	return &Agent{
		client:    client,
		modelName: p.Config.GeminiModel,
		log:       p.Log.Named("agent"),
	}, nil
}

// Run executes the autonomous agent loop against the locally cloned repository at repoPath.
// It returns the full list of extracted API endpoints when the agent calls submit_apis.
func (a *Agent) Run(ctx context.Context, repoPath string) ([]*ExtractedAPI, error) {
	model := a.client.GenerativeModel(a.modelName)
	model.Tools = buildTools()
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	session := model.StartChat()
	executor := newToolExecutor(repoPath, a.log)

	initialMsg := fmt.Sprintf(
		"Analyse this Node.js/Express.js repository and extract all API endpoints.\n"+
			"The repository has been cloned to: %s\n\n"+
			"Start by calling list_directory(\".\") to orient yourself, then follow the strategy in your instructions.",
		repoPath,
	)

	a.log.Info("starting agent loop", zap.String("model", a.modelName), zap.String("repo", repoPath))

	resp, err := session.SendMessage(ctx, genai.Text(initialMsg))
	if err != nil {
		return nil, fmt.Errorf("initial agent message: %w", err)
	}

	var apis []*ExtractedAPI

	for iteration := 0; iteration < maxIterations; iteration++ {
		a.log.Info("agent iteration", zap.Int("iteration", iteration+1))

		// Guard: empty response means the model has nothing more to say
		if resp == nil || len(resp.Candidates) == 0 {
			a.log.Warn("model returned empty response — ending loop",
				zap.Int("iteration", iteration+1),
			)
			break
		}

		var toolResponses []genai.Part
		done := false

		for _, candidate := range resp.Candidates {
			if candidate.Content == nil {
				continue
			}
			for _, part := range candidate.Content.Parts {
				switch v := part.(type) {

				case genai.FunctionCall:
					a.log.Info("tool call",
						zap.String("tool", v.Name),
						zap.Any("args", sanitiseArgs(v.Args)),
					)

					if v.Name == "submit_apis" {
						extracted, parseErr := parseSubmitAPIs(v.Args, a.log)
						if parseErr != nil {
							a.log.Error("submit_apis parse error", zap.Error(parseErr))
							toolResponses = append(toolResponses, genai.FunctionResponse{
								Name:     v.Name,
								Response: map[string]any{"error": parseErr.Error()},
							})
							continue
						}
						apis = extracted
						done = true
						a.log.Info("agent submitted APIs", zap.Int("count", len(apis)))
						toolResponses = append(toolResponses, genai.FunctionResponse{
							Name:     v.Name,
							Response: map[string]any{"status": "received", "count": len(apis)},
						})

					} else {
						result, toolErr := executor.Execute(v.Name, v.Args)
						errStr := ""
						if toolErr != nil {
							a.log.Warn("tool error", zap.String("tool", v.Name), zap.Error(toolErr))
							errStr = toolErr.Error()
						}
						toolResponses = append(toolResponses, genai.FunctionResponse{
							Name: v.Name,
							Response: map[string]any{
								"result": result,
								"error":  errStr,
							},
						})
					}

				case genai.Text:
					if s := string(v); s != "" {
						a.log.Info("agent reasoning", zap.String("text", truncateLog(s, 300)))
					}
				}
			}
		}

		if done {
			break
		}

		if len(toolResponses) == 0 {
			a.log.Warn("agent stopped issuing tool calls before submit_apis — ending loop")
			break
		}

		resp, err = session.SendMessage(ctx, toolResponses...)
		if err != nil {
			return nil, fmt.Errorf("send tool responses (iteration %d): %w", iteration+1, err)
		}
	}

	if len(apis) == 0 {
		a.log.Warn("agent loop ended without a submit_apis call — no endpoints captured")
	}
	a.log.Info("agent loop complete", zap.Int("endpoints_extracted", len(apis)))
	return apis, nil
}

// buildTools returns the Gemini tool declarations for the agent.
func buildTools() []*genai.Tool {
	str := func(desc string) *genai.Schema {
		return &genai.Schema{Type: genai.TypeString, Description: desc}
	}
	obj := func(props map[string]*genai.Schema, required []string) *genai.Schema {
		return &genai.Schema{Type: genai.TypeObject, Properties: props, Required: required}
	}

	return []*genai.Tool{
		{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Name:        "list_directory",
					Description: "List files and sub-directories at the given path (relative to repo root). Use \".\" for root.",
					Parameters: obj(map[string]*genai.Schema{
						"path": str("Relative path from repository root. Use \".\" for root directory."),
					}, []string{"path"}),
				},
				{
					Name:        "read_file",
					Description: "Read the full contents of a file (relative to repo root). Large files are automatically truncated.",
					Parameters: obj(map[string]*genai.Schema{
						"path": str("Relative file path from repository root."),
					}, []string{"path"}),
				},
				{
					Name:        "search_code",
					Description: "Case-insensitive grep across all .js / .ts / .mjs files in the repository (node_modules excluded). Returns matching lines with file:line references.",
					Parameters: obj(map[string]*genai.Schema{
						"pattern": str("Text pattern to search for (case-insensitive)."),
					}, []string{"pattern"}),
				},
				{
					Name:        "submit_apis",
					Description: "Call this ONLY when you have finished analysing all route files. Submit the complete list of extracted API endpoints as a JSON array string.",
					Parameters: obj(map[string]*genai.Schema{
						"apis_json": str("A valid JSON array string of API endpoint objects following the schema defined in your instructions."),
					}, []string{"apis_json"}),
				},
			},
		},
	}
}

// parseSubmitAPIs parses the apis_json argument from the submit_apis tool call.
func parseSubmitAPIs(args map[string]any, log *zap.Logger) ([]*ExtractedAPI, error) {
	raw, ok := args["apis_json"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("apis_json argument is missing or empty")
	}

	var apis []*ExtractedAPI
	if err := json.Unmarshal([]byte(raw), &apis); err != nil {
		log.Error("invalid apis_json", zap.String("raw_snippet", truncateLog(raw, 200)))
		return nil, fmt.Errorf("unmarshal apis_json: %w", err)
	}
	return apis, nil
}

// sanitiseArgs removes the apis_json value from log output to avoid huge log lines.
func sanitiseArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if k == "apis_json" {
			out[k] = "[omitted from log]"
		} else {
			out[k] = v
		}
	}
	return out
}

// truncateLog truncates a string to maxLen for safe log output.
func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// Module registers the agent package with Uber FX.
var Module = fx.Module("agent",
	fx.Provide(New),
)
