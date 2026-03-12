package config

import (
	"fmt"

	"github.com/spf13/viper"
	"go.uber.org/fx"
)

// Config holds all application configuration.
// Values are resolved in this priority order (highest first):
//  1. OS environment variables
//  2. .env file
//  3. Defaults defined below
type Config struct {
	LLMProvider string
	LLMAPIKey   string
	LLMModel    string
	OutputDir   string
}

// New loads configuration using Viper.
// Reads from .env (if present) and OS environment variables.
// .env is optional — the file not existing is not an error.
func New() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("LLM_PROVIDER", "openai")
	v.SetDefault("OUTPUT_DIR", "output")

	// Read from .env file (KEY=VALUE format, no section headers)
	v.SetConfigFile(".env")
	v.SetConfigType("env")

	// Also bind OS environment variables — these take priority over .env
	v.AutomaticEnv()

	// Load .env — ignore "file not found" but surface actual parse errors
	if err := v.ReadInConfig(); err != nil {
		if _, notFound := err.(viper.ConfigFileNotFoundError); !notFound {
			// A real parse error — tell the user
			return nil, fmt.Errorf("parse .env: %w", err)
		}
		// File simply doesn't exist — that's fine, rely on OS env vars
	}

	// LLM_API_KEY with fallback to OPENAI_API_KEY for backward compat
	apiKey := v.GetString("LLM_API_KEY")
	if apiKey == "" {
		apiKey = v.GetString("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("LLM_API_KEY is not set — add it to .env or export it as an environment variable (OPENAI_API_KEY is also accepted)")
	}

	// LLM_MODEL with fallback to OPENAI_MODEL for backward compat
	model := v.GetString("LLM_MODEL")
	if model == "" {
		model = v.GetString("OPENAI_MODEL")
	}
	if model == "" {
		model = "gpt-5-mini"
	}

	return &Config{
		LLMProvider: v.GetString("LLM_PROVIDER"),
		LLMAPIKey:   apiKey,
		LLMModel:    model,
		OutputDir:   v.GetString("OUTPUT_DIR"),
	}, nil
}

// Module registers the config package with Uber FX.
var Module = fx.Module("config",
	fx.Provide(New),
)
