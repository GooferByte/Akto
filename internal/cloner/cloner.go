package cloner

import (
	"context"
	"fmt"
	"os"

	"github.com/go-git/go-git/v5"
	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Result holds the outcome of a successful clone operation.
type Result struct {
	LocalPath string
}

// Cloner clones remote git repositories to a temporary local directory.
type Cloner struct {
	log *zap.Logger
}

// New creates a new Cloner instance.
func New(log *zap.Logger) *Cloner {
	return &Cloner{log: log.Named("cloner")}
}

// Clone performs a shallow clone of repoURL into a temp directory.
// The caller is responsible for cleaning up Result.LocalPath via os.RemoveAll.
func (c *Cloner) Clone(ctx context.Context, repoURL string) (*Result, error) {
	tmpDir, err := os.MkdirTemp("", "akto-repo-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	c.log.Info("cloning repository", zap.String("url", repoURL), zap.String("dest", tmpDir))

	opts := &git.CloneOptions{
		URL:          repoURL,
		Depth:        1,    // shallow clone — we only need the latest snapshot
		SingleBranch: true, // only fetch the default branch for speed
		Progress:     os.Stdout,
	}

	// If a GITHUB_TOKEN env var is set, use it to avoid rate-limiting on private repos
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		opts.Auth = &gogithttp.BasicAuth{Username: "token", Password: token}
	}

	// PlainCloneContext honours the context so the operation can be cancelled
	// (e.g. if the user hits Ctrl-C during a long clone).
	if _, err = git.PlainCloneContext(ctx, tmpDir, false, opts); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("git clone %s: %w", repoURL, err)
	}

	c.log.Info("repository cloned successfully",
		zap.String("path", tmpDir),
	)
	return &Result{LocalPath: tmpDir}, nil
}

// Module registers the cloner package with Uber FX.
var Module = fx.Module("cloner",
	fx.Provide(New),
)
