package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GooferByte/Akto/internal/config"
	"github.com/GooferByte/Akto/internal/llm"
	"go.uber.org/fx"
	"go.uber.org/zap"
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
5. **BATCH read_file calls**: read multiple route files in a SINGLE turn by issuing many read_file tool calls at once — do NOT read them one at a time.
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
When you are finished, call submit_apis with apis_json set to a valid JSON array string.

**CRITICAL JSON RULES:**
- All strings MUST use straight double quotes, never curly/smart quotes.
- Escape special characters inside strings: use \" for quotes, \n for newlines, \\ for backslashes.
- Do NOT use trailing commas after the last item in arrays or objects.
- Keep descriptions short (under 80 chars) to avoid escaping issues.
- If you have many endpoints (30+), you may call submit_apis multiple times in smaller batches — each call appends to the results.

Example of ONE endpoint object:
{"method":"GET","path":"/api/items","description":"List all items","tags":["Items"],"path_params":[],"query_params":[{"name":"q","description":"Search term","required":false,"type":"string"}],"request_body":null,"response":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}},"requires_auth":false,"content_type":"application/json"}

Be thorough — do NOT call submit_apis until you have covered every route file in the repository.`
)

// Agent runs an autonomous tool-calling loop powered by an LLM to extract API endpoints.
type Agent struct {
	client    llm.Client
	modelName string
	log       *zap.Logger
}

// AgentParams groups Agent dependencies for Uber FX injection.
type AgentParams struct {
	fx.In

	LLMClient llm.Client
	Config    *config.Config
	Log       *zap.Logger
}

// New creates an Agent backed by the configured LLM provider.
func New(p AgentParams) (*Agent, error) {
	return &Agent{
		client:    p.LLMClient,
		modelName: p.Config.LLMModel,
		log:       p.Log.Named("agent"),
	}, nil
}

// Run executes the autonomous agent loop against the locally cloned repository at repoPath.
// It returns the full list of extracted API endpoints when the agent calls submit_apis.
func (a *Agent) Run(ctx context.Context, repoPath string) ([]*ExtractedAPI, error) {
	tools := buildTools()
	executor := newToolExecutor(repoPath, a.log)

	initialMsg := fmt.Sprintf(
		"Analyse this Node.js/Express.js repository and extract all API endpoints.\n"+
			"The repository has been cloned to: %s\n\n"+
			"Start by calling list_directory(\".\") to orient yourself, then follow the strategy in your instructions.",
		repoPath,
	)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: initialMsg},
	}

	a.log.Info("starting agent loop", zap.String("model", a.modelName), zap.String("repo", repoPath))

	var apis []*ExtractedAPI

	for iteration := 0; iteration < maxIterations; iteration++ {
		a.log.Info("agent iteration", zap.Int("iteration", iteration+1))

		resp, err := a.client.ChatCompletion(ctx, a.modelName, messages, tools)
		if err != nil {
			return nil, fmt.Errorf("chat completion (iteration %d): %w", iteration+1, err)
		}

		if resp.Content == "" && len(resp.ToolCalls) == 0 {
			a.log.Warn("model returned empty response — ending loop", zap.Int("iteration", iteration+1))
			break
		}

		// Log any text content from the assistant
		if resp.Content != "" {
			a.log.Info("agent reasoning", zap.String("text", truncateLog(resp.Content, 300)))
		}

		// Append the assistant's message to the conversation history
		messages = append(messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// If no tool calls, the model is done
		if len(resp.ToolCalls) == 0 {
			a.log.Warn("agent stopped issuing tool calls before submit_apis — ending loop")
			break
		}

		done := false

		for _, toolCall := range resp.ToolCalls {
			name := toolCall.Function

			// Parse arguments from JSON string
			var args map[string]any
			if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
				a.log.Error("failed to parse tool arguments",
					zap.String("tool", name),
					zap.Error(err),
				)
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					Content:    fmt.Sprintf(`{"error": "invalid arguments: %s"}`, err.Error()),
					ToolCallID: toolCall.ID,
				})
				continue
			}

			a.log.Info("tool call",
				zap.String("tool", name),
				zap.Any("args", sanitiseArgs(args)),
			)

			if name == "submit_apis" {
				extracted, parseErr := parseSubmitAPIs(args, a.log)
				if parseErr != nil {
					a.log.Error("submit_apis parse error", zap.Error(parseErr))
					messages = append(messages, llm.Message{
						Role:       llm.RoleTool,
						Content:    fmt.Sprintf(`{"error": %q, "hint": "Your JSON is invalid. Fix the escaping and resubmit ALL endpoints. Do NOT submit a partial list or a single endpoint — resubmit the complete list with valid JSON."}`, parseErr.Error()),
						ToolCallID: toolCall.ID,
					})
					continue
				}
				apis = append(apis, extracted...)
				done = true
				a.log.Info("agent submitted APIs", zap.Int("count", len(apis)))
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					Content:    fmt.Sprintf(`{"status": "received", "count": %d, "total_so_far": %d}`, len(extracted), len(apis)),
					ToolCallID: toolCall.ID,
				})
			} else {
				result, toolErr := executor.Execute(name, args)
				errStr := ""
				if toolErr != nil {
					a.log.Warn("tool error", zap.String("tool", name), zap.Error(toolErr))
					errStr = toolErr.Error()
				}
				respJSON, _ := json.Marshal(map[string]any{
					"result": result,
					"error":  errStr,
				})
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					Content:    string(respJSON),
					ToolCallID: toolCall.ID,
				})
			}
		}

		if done {
			break
		}
	}

	if len(apis) == 0 {
		a.log.Warn("agent loop ended without a submit_apis call — no endpoints captured")
	}
	a.log.Info("agent loop complete", zap.Int("endpoints_extracted", len(apis)))
	return apis, nil
}

// buildTools returns the tool declarations for the agent.
func buildTools() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name:        "list_directory",
			Description: "List files and sub-directories at the given path (relative to repo root). Use \".\" for root.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]string{
						"type":        "string",
						"description": "Relative path from repository root. Use \".\" for root directory.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "read_file",
			Description: "Read the full contents of a file (relative to repo root). Large files are automatically truncated.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]string{
						"type":        "string",
						"description": "Relative file path from repository root.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "search_code",
			Description: "Case-insensitive grep across all .js / .ts / .mjs files in the repository (node_modules excluded). Returns matching lines with file:line references.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]string{
						"type":        "string",
						"description": "Text pattern to search for (case-insensitive).",
					},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "submit_apis",
			Description: "Call this ONLY when you have finished analysing all route files. Submit the complete list of extracted API endpoints as a JSON array string.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"apis_json": map[string]string{
						"type":        "string",
						"description": "A valid JSON array string of API endpoint objects following the schema defined in your instructions.",
					},
				},
				"required": []string{"apis_json"},
			},
		},
	}
}

// parseSubmitAPIs parses the apis_json argument from the submit_apis tool call.
// It attempts JSON sanitization for common LLM mistakes before parsing.
func parseSubmitAPIs(args map[string]any, log *zap.Logger) ([]*ExtractedAPI, error) {
	raw, ok := args["apis_json"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("apis_json argument is missing or empty")
	}

	// Try parsing as-is first
	var apis []*ExtractedAPI
	if err := json.Unmarshal([]byte(raw), &apis); err != nil {
		// Try sanitizing common LLM JSON mistakes
		sanitized := sanitizeJSON(raw)
		if err2 := json.Unmarshal([]byte(sanitized), &apis); err2 != nil {
			// Provide detailed error context to help the LLM fix it
			errDetail := describeJSONError(raw, err)
			log.Error("invalid apis_json",
				zap.String("raw_snippet", truncateLog(raw, 200)),
				zap.String("error_detail", errDetail),
			)
			return nil, fmt.Errorf("%s", errDetail)
		}
		log.Info("apis_json required sanitization but parsed successfully")
	}
	return apis, nil
}

// sanitizeJSON attempts to fix common LLM JSON mistakes.
func sanitizeJSON(s string) string {
	// Replace curly/smart quotes with straight quotes
	s = strings.ReplaceAll(s, "\u201c", `"`) // left double quote
	s = strings.ReplaceAll(s, "\u201d", `"`) // right double quote
	s = strings.ReplaceAll(s, "\u2018", "'") // left single quote
	s = strings.ReplaceAll(s, "\u2019", "'") // right single quote

	// Remove trailing commas before } or ]
	// Simple approach: replace ,} with } and ,] with ]
	for strings.Contains(s, ",}") || strings.Contains(s, ",]") {
		s = strings.ReplaceAll(s, ",}", "}")
		s = strings.ReplaceAll(s, ",]", "]")
	}

	// Trim whitespace-only issues
	s = strings.TrimSpace(s)

	return s
}

// describeJSONError provides a detailed description of where JSON parsing failed.
func describeJSONError(raw string, err error) string {
	// Try to extract offset from json.UnmarshalTypeError or json.SyntaxError
	var syntaxErr *json.SyntaxError
	if ok := errorAs(err, &syntaxErr); ok {
		offset := int(syntaxErr.Offset)
		context := extractErrorContext(raw, offset)
		return fmt.Sprintf("JSON syntax error at byte %d: %s — context: ...%s...",
			offset, err.Error(), context)
	}

	return fmt.Sprintf("JSON parse error: %s", err.Error())
}

// errorAs is a typed wrapper for errors.As to avoid import.
func errorAs(err error, target **json.SyntaxError) bool {
	if se, ok := err.(*json.SyntaxError); ok {
		*target = se
		return true
	}
	return false
}

// extractErrorContext returns ~60 characters around the given byte offset.
func extractErrorContext(s string, offset int) string {
	start := offset - 30
	if start < 0 {
		start = 0
	}
	end := offset + 30
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
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
