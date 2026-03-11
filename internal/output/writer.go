package output

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GooferByte/Akto/internal/agent"
	"github.com/GooferByte/Akto/internal/config"
	"github.com/GooferByte/Akto/internal/schema"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Writer persists the extraction results to disk.
type Writer struct {
	outputDir string
	log       *zap.Logger
}

// New creates a Writer using the configured output directory.
func New(cfg *config.Config, log *zap.Logger) *Writer {
	return &Writer{
		outputDir: cfg.OutputDir,
		log:       log.Named("output"),
	}
}

// Write saves openapi.json, openapi.yaml, and summary.md to the output directory.
func (w *Writer) Write(_ context.Context, spec *schema.OpenAPISpec, apis []*agent.ExtractedAPI) error {
	if err := os.MkdirAll(w.outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", w.outputDir, err)
	}

	if err := w.writeJSON(spec); err != nil {
		return err
	}
	if err := w.writeYAML(spec); err != nil {
		return err
	}
	if err := w.writeSummary(apis, spec); err != nil {
		return err
	}

	w.log.Info("output written",
		zap.String("dir", w.outputDir),
		zap.Int("endpoints", len(apis)),
	)
	return nil
}

func (w *Writer) writeJSON(spec *schema.OpenAPISpec) error {
	path := filepath.Join(w.outputDir, "openapi.json")
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal openapi JSON: %w", err)
	}
	if err = os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	w.log.Info("wrote openapi.json", zap.String("path", path), zap.Int("bytes", len(data)))
	return nil
}

func (w *Writer) writeYAML(spec *schema.OpenAPISpec) error {
	path := filepath.Join(w.outputDir, "openapi.yaml")
	data, err := yaml.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal openapi YAML: %w", err)
	}
	if err = os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	w.log.Info("wrote openapi.yaml", zap.String("path", path), zap.Int("bytes", len(data)))
	return nil
}

func (w *Writer) writeSummary(apis []*agent.ExtractedAPI, spec *schema.OpenAPISpec) error {
	path := filepath.Join(w.outputDir, "summary.md")

	var sb strings.Builder

	sb.WriteString("# API Extraction Report\n\n")
	fmt.Fprintf(&sb, "**Generated:** %s  \n", time.Now().Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&sb, "**Total Endpoints:** %d  \n", len(apis))
	fmt.Fprintf(&sb, "**OpenAPI Version:** %s  \n\n", spec.OpenAPI)

	// Group by tag
	tagMap := make(map[string][]*agent.ExtractedAPI)
	untagged := []*agent.ExtractedAPI{}
	for _, api := range apis {
		if len(api.Tags) == 0 {
			untagged = append(untagged, api)
			continue
		}
		for _, tag := range api.Tags {
			tagMap[tag] = append(tagMap[tag], api)
		}
	}

	tags := make([]string, 0, len(tagMap))
	for t := range tagMap {
		tags = append(tags, t)
	}
	sort.Strings(tags)

	sb.WriteString("## Endpoints by Tag\n\n")
	for _, tag := range tags {
		endpoints := tagMap[tag]
		fmt.Fprintf(&sb, "### %s (%d)\n\n", tag, len(endpoints))
		sb.WriteString("| Method | Path | Auth | Description |\n")
		sb.WriteString("|--------|------|------|-------------|\n")
		for _, ep := range endpoints {
			auth := "No"
			if ep.RequiresAuth {
				auth = "Yes"
			}
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s |\n",
				strings.ToUpper(ep.Method), ep.Path, auth, truncate(ep.Description, 80))
		}
		sb.WriteString("\n")
	}

	if len(untagged) > 0 {
		fmt.Fprintf(&sb, "### Uncategorised (%d)\n\n", len(untagged))
		sb.WriteString("| Method | Path | Auth | Description |\n")
		sb.WriteString("|--------|------|------|-------------|\n")
		for _, ep := range untagged {
			auth := "No"
			if ep.RequiresAuth {
				auth = "Yes"
			}
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s |\n",
				strings.ToUpper(ep.Method), ep.Path, auth, truncate(ep.Description, 80))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Output Files\n\n")
	sb.WriteString("| File | Description |\n")
	sb.WriteString("|------|-------------|\n")
	sb.WriteString("| `openapi.json` | OpenAPI 3.0 specification (JSON format) |\n")
	sb.WriteString("| `openapi.yaml` | OpenAPI 3.0 specification (YAML format) |\n")
	sb.WriteString("| `summary.md` | This summary report |\n\n")

	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	w.log.Info("wrote summary.md", zap.String("path", path))
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// Module registers the output package with Uber FX.
var Module = fx.Module("output",
	fx.Provide(New),
)
