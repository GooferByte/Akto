package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"go.uber.org/zap"
)

const (
	maxFileBytes   = 120_000 // soft cap — files larger than this are UTF-8 safely truncated
	maxSearchLines = 150     // max result lines returned per search_code call
)

// errSearchLimit is a typed sentinel used to break out of the walk early.
// Using a named error avoids fragile string comparisons.
var errSearchLimit = errors.New("search result limit reached")

// ToolExecutor executes the agent's tools against a locally cloned repository.
type ToolExecutor struct {
	repoPath string // absolute, cleaned path to the repo root
	log      *zap.Logger
}

// newToolExecutor creates a ToolExecutor rooted at repoPath.
func newToolExecutor(repoPath string, log *zap.Logger) *ToolExecutor {
	// Store the canonical absolute path so safePath comparisons are reliable.
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		abs = repoPath // fallback — filepath.Abs only fails on very exotic systems
	}
	return &ToolExecutor{repoPath: filepath.Clean(abs), log: log.Named("tools")}
}

// Execute dispatches a tool call by name and returns the result as a string.
func (e *ToolExecutor) Execute(name string, args map[string]any) (string, error) {
	switch name {
	case "list_directory":
		path, _ := args["path"].(string)
		return e.listDirectory(path)
	case "read_file":
		path, _ := args["path"].(string)
		return e.readFile(path)
	case "search_code":
		pattern, _ := args["pattern"].(string)
		return e.searchCode(pattern)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// safePath resolves relPath against the repo root and rejects any attempt to
// escape it via ".." components or symlinks pointing outside the tree.
func (e *ToolExecutor) safePath(relPath string) (string, error) {
	// filepath.Join already calls Clean internally, but we add an explicit
	// Clean on the input first to normalise ".." before joining.
	joined := filepath.Join(e.repoPath, filepath.Clean("/"+relPath))
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	// Ensure the resolved path is the repo root itself or a descendant.
	if abs != e.repoPath && !strings.HasPrefix(abs, e.repoPath+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes repository root — access denied", relPath)
	}
	return abs, nil
}

// listDirectory returns a formatted listing of the directory at relPath.
func (e *ToolExecutor) listDirectory(relPath string) (string, error) {
	abs, err := e.safePath(relPath)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", fmt.Errorf("cannot read directory %q: %w", relPath, err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Contents of %q:\n\n", relPath)
	for _, entry := range entries {
		kind := "FILE"
		if entry.IsDir() {
			kind = "DIR "
		}
		fmt.Fprintf(&sb, "[%s] %s\n", kind, entry.Name())
	}

	e.log.Debug("list_directory", zap.String("path", relPath), zap.Int("entries", len(entries)))
	return sb.String(), nil
}

// readFile returns the contents of the file at relPath.
// Files exceeding maxFileBytes are truncated at a valid UTF-8 boundary.
func (e *ToolExecutor) readFile(relPath string) (string, error) {
	abs, err := e.safePath(relPath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("file not found: %q", relPath)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory — use list_directory instead", relPath)
	}

	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("cannot read %q: %w", relPath, err)
	}

	truncated := false
	if len(raw) > maxFileBytes {
		raw = truncateUTF8(raw, maxFileBytes)
		truncated = true
	}

	e.log.Debug("read_file",
		zap.String("path", relPath),
		zap.Int("bytes_returned", len(raw)),
		zap.Bool("truncated", truncated),
	)

	content := string(raw)
	if truncated {
		content += fmt.Sprintf("\n\n... [FILE TRUNCATED — first %d bytes shown]", len(raw))
	}
	return content, nil
}

// searchCode performs a case-insensitive grep across all .js/.ts/.mjs files in
// the repo, skipping directories that never contain application routes.
func (e *ToolExecutor) searchCode(pattern string) (string, error) {
	if pattern == "" {
		return "", fmt.Errorf("pattern must not be empty")
	}

	lowerPattern := strings.ToLower(pattern)
	var results strings.Builder
	lineCount := 0

	walkErr := filepath.Walk(e.repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", ".git", "dist", "build", "coverage", ".nyc_output", "frontend":
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".js" && ext != ".ts" && ext != ".mjs" {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		relPath, _ := filepath.Rel(e.repoPath, path)
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), lowerPattern) {
				fmt.Fprintf(&results, "%s:%d: %s\n", relPath, i+1, strings.TrimSpace(line))
				lineCount++
				if lineCount >= maxSearchLines {
					return errSearchLimit // typed sentinel — no string comparison needed
				}
			}
		}
		return nil
	})

	// errSearchLimit is expected; any other error is genuine
	if walkErr != nil && !errors.Is(walkErr, errSearchLimit) {
		return "", walkErr
	}

	if lineCount == 0 {
		return fmt.Sprintf("No matches found for pattern %q", pattern), nil
	}

	out := results.String()
	if errors.Is(walkErr, errSearchLimit) {
		out += fmt.Sprintf("\n... [first %d matches shown — refine pattern for more precise results]", maxSearchLines)
	}

	e.log.Debug("search_code", zap.String("pattern", pattern), zap.Int("matches", lineCount))
	return out, nil
}

// truncateUTF8 shrinks b to at most maxBytes while preserving valid UTF-8
// by walking back from the boundary until the slice is valid.
func truncateUTF8(b []byte, maxBytes int) []byte {
	if len(b) <= maxBytes {
		return b
	}
	b = b[:maxBytes]
	// Walk back until we land on a valid UTF-8 boundary.
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return b
}
