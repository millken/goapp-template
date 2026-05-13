package commands

import (
	"context"
	"log/slog"
	"os"

	"github.com/joho/godotenv"
	"github.com/millken/goapp-template/internal/buildinfo"
	"github.com/millken/goapp-template/internal/config"
	phuslog "github.com/phuslu/log"
	"github.com/spf13/cobra"
)

type appConfigKey struct{}

// AppInit performs shared initialization (env loading, config, logging).
// Subcommands that define their own PersistentPreRunE must call this explicitly:
//
//	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
//	    return AppInit(cmd, args)
//	},
func AppInit(cmd *cobra.Command, args []string) error {
	// godotenv.Load wraps errors, so use os.Stat to check existence first.
	if _, err := os.Stat(EnvFile()); err == nil {
		if err := godotenv.Load(EnvFile()); err != nil {
			slog.Warn("failed to load .env", "path", EnvFile(), "err", err)
		}
	}

	cfg, cfgPath, err := loadConfig(cmd)
	if err != nil {
		slog.Warn("failed to load config", "path", cfgPath, "err", err)
	}

	verbose, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	if verbose {
		cfg.Log.Level = "debug"
	}

	cmd.SetContext(context.WithValue(cmd.Context(), appConfigKey{}, cfg))
	setupLogging(cfg.Log)
	slog.Debug("logging initialized", "level", cfg.Log.Level, "format", cfg.Log.Format)
	return nil
}

func loadConfig(cmd *cobra.Command) (config.Config, string, error) {
	cfgPath := resolveConfigFile(cmd)
	cfg, err := config.Load(cfgPath)
	return cfg, cfgPath, err
}

func configFromContext(ctx context.Context) (config.Config, bool) {
	cfg, ok := ctx.Value(appConfigKey{}).(config.Config)
	return cfg, ok
}

func New() *cobra.Command {
	root := &cobra.Command{
		Use:               buildinfo.AppName,
		Short:             "A Go application",
		SilenceUsage:      true,
		DisableAutoGenTag: true,
		PersistentPreRunE: AppInit,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.PersistentFlags().BoolP("verbose", "v", false, "Enable debug logs")
	root.PersistentFlags().StringP("config", "c", "", "Config file (overrides "+homeEnvVar()+" and default path)")
	root.CompletionOptions.HiddenDefaultCmd = true

	root.AddCommand(newVersionCmd())
	root.AddCommand(newServeCmd())

	return root
}

func setupLogging(cfg config.LogConfig) {
	var level phuslog.Level
	switch cfg.Level {
	case "debug":
		level = phuslog.DebugLevel
	case "warn":
		level = phuslog.WarnLevel
	case "error":
		level = phuslog.ErrorLevel
	default:
		level = phuslog.InfoLevel
	}

	var writer phuslog.Writer
	if cfg.File.Path != "" {
		writer = &phuslog.FileWriter{
			Filename:     cfg.File.Path,
			MaxSize:      cfg.File.MaxSize,
			MaxBackups:   cfg.File.MaxBackups,
			LocalTime:    cfg.File.LocalTime,
			EnsureFolder: true,
		}
	} else if cfg.Format == "json" || !phuslog.IsTerminal(os.Stderr.Fd()) {
		writer = &phuslog.IOWriter{Writer: os.Stderr}
	} else {
		writer = &phuslog.ConsoleWriter{
			ColorOutput:    true,
			QuoteString:    true,
			EndWithMessage: true,
			Writer:         os.Stderr,
		}
	}

	logger := &phuslog.Logger{
		Level:  level,
		Writer: writer,
	}
	slog.SetDefault(logger.Slog())
}

// resolveConfigFile returns the config file path using the following priority:
//  1. --config flag
//  2. ConfigFile() which derives from Home() (itself respects $MYAPP_HOME)
func resolveConfigFile(cmd *cobra.Command) string {
	if p, _ := cmd.Root().PersistentFlags().GetString("config"); p != "" {
		return p
	}
	return ConfigFile()
}
