# Akto — Autonomous API Extraction Agent

An autonomous AI agent that accepts any GitHub repository URL and extracts all REST API endpoints with full request/response schemas, outputting an **OpenAPI 3.0** specification.

Built using **Go**, **Uber FX**, and **Zap**.

---

## How It Works

```
GitHub URL
    │
    ▼
┌─────────┐    ┌──────────────────────────────────────────────┐    ┌──────────────┐
│  Cloner │───▶│       Autonomous AI Agent (tool-calling)     │───▶│ Schema Build │
│  git    │    │                                              │    │  OpenAPI 3.0 │
└─────────┘    │  Tools available to the agent:               │    └──────┬───────┘
               │  • list_directory(path)                      │           │
               │  • read_file(path)                           │           ▼
               │  • search_code(pattern)                      │    ┌──────────────┐
               │  • submit_apis(apis_json)                    │    │    Output    │
               └──────────────────────────────────────────────┘    │ openapi.json │
                                                                   │ openapi.yaml │
                                                                   │ summary.md   │
                                                                   └──────────────┘
```

The agent autonomously navigates the repository — reading route files, tracing router mounts, and inferring schemas — until it has covered all Express.js endpoints, then submits the complete list via the `submit_apis` tool.

---

## Prerequisites

- **Go 1.23+**
- **AI API key** — configured via `.env`
- **git** *(optional)* — only needed if `GITHUB_TOKEN` is not set and the repo is private

---

## Setup

```bash
# 1. Clone this repo
git clone https://github.com/GooferByte/Akto.git
cd Akto

# 2. Configure your API key
cp .env.example .env
# Edit .env and fill in the required values

# 3. Install dependencies
go mod download

# 4. Build  (binary written to bin/akto)
go build -o bin/akto ./cmd/akto
```

---

## Usage

```bash
./bin/akto <github-repo-url>
```

**Example:**

```bash
./bin/akto https://github.com/your-org/your-repo
```

The agent will:
1. Clone the repository (shallow, depth=1)
2. Autonomously explore all route files
3. Extract every API endpoint with schemas
4. Write results to the `output/` directory

---

## Output

All output files are written to the `output/` directory (configurable via `OUTPUT_DIR` in `.env`):

| File | Description |
|------|-------------|
| `output/openapi.json` | Full OpenAPI 3.0 spec (JSON) |
| `output/openapi.yaml` | Full OpenAPI 3.0 spec (YAML) |
| `output/summary.md` | Human-readable report grouped by tag |

---

## Configuration

All configuration is via `.env` (or environment variables):

| Variable | Default | Description |
|----------|---------|-------------|
| `LLM_PROVIDER` | `openai` | LLM provider to use (`openai`, with more coming) |
| `LLM_API_KEY` | *required* | API key for the configured provider |
| `LLM_MODEL` | `gpt-5-mini` | Model name to use |
| `OUTPUT_DIR` | `output` | Directory for output files |
| `GITHUB_TOKEN` | *(optional)* | Used for private repos or to avoid rate limiting |

> **Backward compat:** `OPENAI_API_KEY` and `OPENAI_MODEL` are still accepted as fallbacks.

---

## Project Structure

```
.
├── cmd/
│   └── akto/
│       └── main.go              # CLI entrypoint + FX app wiring
├── internal/
│   ├── config/config.go         # Configuration loading (Viper + .env)
│   ├── logger/logger.go         # Zap logger + FX event logger
│   ├── cloner/cloner.go         # Shallow git clone via native git binary
│   ├── llm/
│   │   ├── llm.go               # Provider-agnostic LLM client interface
│   │   └── openai.go            # OpenAI adapter implementation
│   ├── agent/
│   │   ├── agent.go             # Autonomous AI tool-calling loop
│   │   ├── tools.go             # Tool executor (list_dir, read_file, search)
│   │   └── types.go             # ExtractedAPI, ExtractedParam types
│   ├── schema/
│   │   ├── builder.go           # Converts extracted APIs → OpenAPI 3.0
│   │   └── types.go             # OpenAPI 3.0 Go type definitions
│   └── output/
│       └── writer.go            # Writes JSON, YAML, and summary.md
├── .env.example                 # Config template
├── .gitignore
└── go.mod
```

---

## Tech Stack

| Concern | Library |
|---------|---------|
| Dependency Injection | [Uber FX](https://github.com/uber-go/fx) |
| Structured Logging | [Uber Zap](https://github.com/uber-go/zap) |
| Config | [Viper](https://github.com/spf13/viper) |
| Git Cloning | [go-git](https://github.com/go-git/go-git) (pure Go) |
| YAML Output | [gopkg.in/yaml.v3](https://pkg.go.dev/gopkg.in/yaml.v3) |
