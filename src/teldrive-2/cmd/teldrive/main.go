package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/tgdrive/teldrive/v2/internal/app"
	"github.com/tgdrive/teldrive/v2/internal/config"
	"github.com/tgdrive/teldrive/v2/internal/logging"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCommand().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	// Cobra's MousetrapHelpText defaults to a non-empty string: any Cobra binary
	// launched from explorer.exe prints "This is a command line tool. You need to
	// open cmd.exe and run it from there.", waits five seconds and exits before a
	// single line of this program runs.
	//
	// That is precisely how the portable bundle is meant to be started, so the
	// splash has to be switched off or double-clicking teldrive.exe -- the one
	// documented entry point -- dies on the launch screen. Launching from a shell
	// is unaffected either way, which is why the regression only shows up for the
	// people the bundle is built for.
	cobra.MousetrapHelpText = ""

	root := &cobra.Command{
		Use:           "teldrive",
		Short:         "Telegram-backed cloud storage server",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Running the binary with no subcommand is how the portable bundle is
		// started: double-clicking teldrive.exe brings up the bundled PostgreSQL
		// and the server, so the released folder needs exactly one entry point.
		RunE: func(cmd *cobra.Command, _ []string) error {
			enableUTF8Console()
			out := cmd.OutOrStdout()
			quiet, _ := cmd.Flags().GetBool("autostart")
			if quiet {
				// Started by the Windows Run key: stay out of the way.
				minimizeConsoleWindow()
			}
			err := launchBundled(cmd.Context(), out, quiet)
			switch {
			case errors.Is(err, errNotBundled):
				writeLauncherHint(out, buildVersion())
				pauseWhenDoubleClicked(os.Stdin, out)
				return nil
			case err != nil:
				fmt.Fprintf(cmd.ErrOrStderr(), "\n启动失败：%v\n", err)
				fmt.Fprintln(out, "详细日志见 data\\initdb.log 与 data\\postgres.log。")
				pauseWhenDoubleClicked(os.Stdin, out)
				return err
			}
			return nil
		},
	}
	root.AddCommand(newRunCommand(), newCheckCommand(), newVersionCommand())
	root.Flags().Bool("autostart", false,
		"启动于登录时：最小化控制台窗口并且不打开浏览器（由开机自启项传入）")
	return root
}

func newRunCommand() *cobra.Command {
	loader := config.NewLoader()
	cmd := &cobra.Command{
		Use:     "run",
		Aliases: []string{"serve"},
		Short:   "Start the TelDrive server",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loader.Load(cmd.Flags())
			if err != nil {
				return err
			}
			logger, err := logging.NewLogger(os.Stdout, cfg.Logging.LogLevel, cfg.Logging.LogFormat)
			if err != nil {
				return err
			}
			slog.SetDefault(logger)
			application, err := app.New(cmd.Context(), cfg, app.Dependencies{Logger: logger, Version: buildVersion()})
			if err != nil {
				return fmt.Errorf("initialize TelDrive: %w", err)
			}
			logger.Info("application.starting", "address", cfg.HTTP.Address, "version", buildVersion(), "commit", commit)
			if err := application.Run(cmd.Context()); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("application.stopped", "error", err)
				return err
			}
			logger.Info("application.stopped")
			return nil
		},
	}
	loader.RegisterFlags(cmd.Flags())
	return cmd
}

func newCheckCommand() *cobra.Command {
	loader := config.NewLoader()
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate configuration and initialize dependencies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loader.Load(cmd.Flags())
			if err != nil {
				return err
			}
			logger, err := logging.NewLogger(os.Stdout, cfg.Logging.LogLevel, cfg.Logging.LogFormat)
			if err != nil {
				return err
			}
			slog.SetDefault(logger)
			application, err := app.New(cmd.Context(), cfg, app.Dependencies{Logger: logger, Version: buildVersion()})
			if err != nil {
				return fmt.Errorf("initialize TelDrive: %w", err)
			}
			if err := application.Close(); err != nil {
				return fmt.Errorf("close checked application: %w", err)
			}
			logger.Info("application.check.succeeded", "version", buildVersion())
			return nil
		},
	}
	loader.RegisterFlags(cmd.Flags())
	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build version information",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			fmt.Printf("teldrive %s commit=%s built=%s\n", buildVersion(), commit, date)
		},
	}
}

func buildVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
