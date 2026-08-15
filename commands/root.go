package commands

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/joho/godotenv"
	"github.com/millken/goapp-template/internal/buildinfo"
	"github.com/millken/goapp-template/internal/config"
	phuslog "github.com/phuslu/log"
	"github.com/spf13/cobra"
)

// appCfg holds the loaded configuration, set by AppInit (PersistentPreRunE) so
// subcommands can read it without context.Value plumbing.
var appCfg config.Config

// AppInit performs shared initialization (env, config, logging). Subcommands
// that define their own PersistentPreRunE must call it explicitly.
func AppInit(cmd *cobra.Command, args []string) error {
	envPath, err := EnvFile()
	if err != nil {
		return fmt.Errorf("resolve env file: %w", err)
	}
	// godotenv.Load wraps errors, so stat first.
	if _, err := os.Stat(envPath); err == nil {
		if err := godotenv.Load(envPath); err != nil {
			slog.Warn("failed to load .env", "path", envPath, "err", err)
		}
	}

	cfg, err := loadConfig(cmd)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	verbose, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	if verbose {
		cfg.Log.Level = "debug"
	}

	// VITE_DEV_ADDR overrides the Vite dev server URL — the single env→config
	// mapping, so serve only deals with flags.
	if v := os.Getenv("VITE_DEV_ADDR"); v != "" {
		cfg.Server.DevAddr = v
	}

	appCfg = cfg
	setupLogging(cfg.Log)
	slog.Debug("logging initialized", "level", cfg.Log.Level, "format", cfg.Log.Format)
	return nil
}

func loadConfig(cmd *cobra.Command) (config.Config, error) {
	cfgPath, err := resolveConfigFile(cmd)
	if err != nil {
		return config.Config{}, fmt.Errorf("resolve config file: %w", err)
	}
	cfg, err := config.Load(cfgPath)
	return cfg, err
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
	//goappctl:admin
	root.AddCommand(newAdminCmd())
	//goappctl:end
	//goappctl:queue
	root.AddCommand(newQueueCmd())
	//goappctl:end

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

// resolveConfigFile returns the config path from --config, else ConfigFile().
func resolveConfigFile(cmd *cobra.Command) (string, error) {
	if p, _ := cmd.Root().PersistentFlags().GetString("config"); p != "" {
		return p, nil
	}
	return ConfigFile()
}
