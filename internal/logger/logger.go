package logger

import (
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
)

// New creates a development-friendly zap logger with coloured, human-readable output.
func New() (*zap.Logger, error) {
	return zap.NewDevelopment()
}

// FxEventLogger returns a zap-backed fxevent.Logger so FX lifecycle
// events are routed through the same structured logger.
func FxEventLogger(log *zap.Logger) fxevent.Logger {
	return &fxevent.ZapLogger{Logger: log.Named("fx")}
}

// Module registers the logger package with Uber FX.
var Module = fx.Module("logger",
	fx.Provide(New),
)
