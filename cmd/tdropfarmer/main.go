package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Du-vy/TDropFarmer/internal/app"
	"github.com/Du-vy/TDropFarmer/internal/auth"
	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/logging"
	"github.com/Du-vy/TDropFarmer/internal/store"
)

const version = "0.1.0-dev"

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCode(err))
	}
}

func run(args []string) error {
	if len(args) < 2 {
		usage(args[0])
		return errUsage
	}

	switch args[1] {
	case "run":
		return runCommand(args[1:])
	case "login":
		return loginCommand(args[1:])
	case "validate":
		return validateCommand(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		usage(args[0])
		return nil
	default:
		usage(args[0])
		return fmt.Errorf("unknown command %q: %w", args[1], errUsage)
	}
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "./config.json", "path to JSON config")
	logLevel := fs.String("log-level", "", "override log level: debug, info, warn, error")
	dryRun := fs.Bool("dry-run", false, "enable dry-run mode")
	dataDir := fs.String("data-dir", "", "override data directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := loadConfigWithOverrides(*configPath, *logLevel, *dryRun, *dataDir)
	if err != nil {
		return err
	}

	logSetup, err := logging.Setup(cfg.Logging)
	if err != nil {
		return err
	}
	defer logSetup.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application := app.New(cfg, logSetup.Logger, store.NewTokenStore(cfg.Storage.Path))
	return application.Run(ctx)
}

func loginCommand(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "./config.json", "path to JSON config")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	logSetup, err := logging.Setup(cfg.Logging)
	if err != nil {
		return err
	}
	defer logSetup.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tokenStore := store.NewTokenStore(cfg.Storage.Path)
	flow := auth.DeviceFlow{
		ClientID: cfg.Auth.ClientID,
		Scopes:   cfg.Auth.Scopes,
		Store:    tokenStore,
	}

	if _, validation, err := flow.ValidToken(ctx); err == nil {
		logSetup.Logger.Info("existing token is valid",
			slog.String("login", validation.Login),
			slog.String("user_id", validation.UserID),
			slog.Int("expires_in", validation.ExpiresIn),
		)
		return nil
	} else if !errors.Is(err, store.ErrTokenNotFound) {
		logSetup.Logger.Warn("existing token could not be used; starting device login", slog.String("error", err.Error()))
	}

	token, err := flow.Login(ctx, func(prompt auth.DevicePrompt) {
		fmt.Fprintf(os.Stdout, "Open %s and enter code %s\n", prompt.VerificationURI, prompt.UserCode)
		fmt.Fprintf(os.Stdout, "Code expires in %s\n", prompt.ExpiresIn.Round(0))
	})
	if err != nil {
		return err
	}

	logSetup.Logger.Info("login completed", slog.Time("expires_at", token.ExpiresAt))
	return nil
}

func validateCommand(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "./config.json", "path to JSON config")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if _, err := config.Load(*configPath); err != nil {
		return err
	}

	fmt.Printf("config %s is valid\n", *configPath)
	return nil
}

func loadConfigWithOverrides(path, logLevel string, dryRun bool, dataDir string) (config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, err
	}
	if logLevel != "" {
		cfg.Logging.Level = logLevel
	}
	if dryRun {
		cfg.Features.DryRun = config.Bool(true)
	}
	if dataDir != "" {
		cfg.Storage.Path = dataDir
	}
	return cfg, nil
}

func usage(program string) {
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [flags]\n\n", program)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  run       start the farmer")
	fmt.Fprintln(os.Stderr, "  login     authenticate with Twitch")
	fmt.Fprintln(os.Stderr, "  validate  validate config")
	fmt.Fprintln(os.Stderr, "  version   print version")
}

var errUsage = errors.New("usage error")

func exitCode(err error) int {
	if errors.Is(err, errUsage) || errors.Is(err, flag.ErrHelp) {
		return 2
	}
	return 1
}
