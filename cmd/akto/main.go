package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/GooferByte/Akto/internal/agent"
	"github.com/GooferByte/Akto/internal/cloner"
	"github.com/GooferByte/Akto/internal/config"
	"github.com/GooferByte/Akto/internal/logger"
	"github.com/GooferByte/Akto/internal/output"
	"github.com/GooferByte/Akto/internal/schema"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage:   akto <github-repo-url>")
		fmt.Fprintln(os.Stderr, "example: akto https://github.com/juice-shop/juice-shop")
		os.Exit(1)
	}
	repoURL := os.Args[1]

	// Populate lets us extract resolved dependencies out of the FX container
	// so we can drive the pipeline sequentially (start → run → stop).
	// This avoids the anti-pattern of spawning a goroutine inside an OnStart
	// hook and calling os.Exit(), which skips OnStop cleanup (e.g. closing the
	// Gemini gRPC client).
	var (
		c   *cloner.Cloner
		a   *agent.Agent
		b   *schema.Builder
		w   *output.Writer
		log *zap.Logger
	)

	app := fx.New(
		fx.WithLogger(func(l *zap.Logger) fxevent.Logger {
			return logger.FxEventLogger(l)
		}),
		config.Module,
		logger.Module,
		cloner.Module,
		agent.Module,
		schema.Module,
		output.Module,
		fx.Populate(&c, &a, &b, &w, &log),
	)

	// ── Start: initialise all modules (wires DI, registers lifecycle hooks) ──
	startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		fmt.Fprintf(os.Stderr, "startup failed: %v\n", err)
		os.Exit(1)
	}

	// ── Pipeline: run synchronously so panics/errors propagate cleanly ──
	pipelineErr := runPipeline(repoURL, c, a, b, w, log)

	// ── Stop: graceful shutdown — OnStop hooks run here (closes Gemini client) ──
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	if err := app.Stop(stopCtx); err != nil {
		log.Error("shutdown error", zap.Error(err))
	}

	if pipelineErr != nil {
		log.Error("pipeline failed", zap.Error(pipelineErr))
		os.Exit(1)
	}
}

// runPipeline executes the four-stage extraction pipeline:
//  1. Clone the repository
//  2. Run the autonomous Gemini agent
//  3. Build the OpenAPI 3.0 spec
//  4. Write all output files
func runPipeline(
	repoURL string,
	c *cloner.Cloner,
	a *agent.Agent,
	b *schema.Builder,
	w *output.Writer,
	log *zap.Logger,
) error {
	ctx := context.Background()

	log.Info("=== Akto API Extraction Agent ===", zap.String("repo", repoURL))

	// 1 — Clone
	log.Info("step 1/4: cloning repository")
	cloneResult, err := c.Clone(ctx, repoURL)
	if err != nil {
		return fmt.Errorf("step 1 clone: %w", err)
	}
	defer func() {
		log.Info("cleaning up cloned repository", zap.String("path", cloneResult.LocalPath))
		_ = os.RemoveAll(cloneResult.LocalPath)
	}()

	// 2 — Agent
	log.Info("step 2/4: running autonomous extraction agent")
	endpoints, err := a.Run(ctx, cloneResult.LocalPath)
	if err != nil {
		return fmt.Errorf("step 2 agent: %w", err)
	}
	if len(endpoints) == 0 {
		log.Warn("agent returned zero endpoints — the repo may not contain Express.js routes")
	}

	// 3 — Schema
	log.Info("step 3/4: building OpenAPI 3.0 specification", zap.Int("endpoints", len(endpoints)))
	spec := b.Build(endpoints, repoURL)

	// 4 — Output
	log.Info("step 4/4: writing output files")
	if err = w.Write(ctx, spec, endpoints); err != nil {
		return fmt.Errorf("step 4 output: %w", err)
	}

	log.Info("=== extraction complete ===",
		zap.Int("total_endpoints", len(endpoints)),
		zap.Int("total_paths", len(spec.Paths)),
	)
	return nil
}
