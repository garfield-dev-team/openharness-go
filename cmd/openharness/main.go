// Package main provides the openharness CLI entry point.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/ui"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		flagModel          string
		flagAPIKey         string
		flagBaseURL        string
		flagMaxTokens      int
		flagSystemPrompt   string
		flagPermissionMode string
		flagOutputFormat   string
		flagVerbose        bool
		flagFast           bool
		flagEffort         string
		flagPasses         int
		flagPrint          bool
		flagPrompt         string
		flagResume         string
		flagContinue       bool
	)

	root := &cobra.Command{
		Use:   "openharness",
		Short: "OpenHarness – AI coding assistant CLI",
		Long:  "OpenHarness is an open-source AI coding assistant powered by Anthropic's Claude.",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil {
				return fmt.Errorf("load settings: %w", err)
			}
			applyFlags(&settings, flagModel, flagAPIKey, flagBaseURL, flagMaxTokens,
				flagSystemPrompt, flagPermissionMode, flagOutputFormat,
				flagVerbose, flagFast, flagEffort, flagPasses)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			// -p prompt: non-interactive single shot
			if flagPrompt != "" {
				return ui.RunPrintMode(ctx, &settings, flagPrompt, flagOutputFormat)
			}

			// --print: read stdin, print response
			if flagPrint {
				prompt := readStdin()
				return ui.RunPrintMode(ctx, &settings, prompt, flagOutputFormat)
			}

			// --resume / --continue: placeholder
			if flagResume != "" || flagContinue {
				return fmt.Errorf("--resume and --continue are not yet implemented")
			}

			// Default: interactive REPL
			return ui.RunREPL(ctx, &settings)
		},
	}

	f := root.Flags()
	f.StringVar(&flagModel, "model", "", "Model name")
	f.StringVar(&flagAPIKey, "api-key", "", "API key")
	f.StringVar(&flagBaseURL, "base-url", "", "Base URL for the API")
	f.IntVar(&flagMaxTokens, "max-tokens", 0, "Maximum output tokens")
	f.StringVar(&flagSystemPrompt, "system-prompt", "", "Custom system prompt")
	f.StringVar(&flagPermissionMode, "permission-mode", "", "Permission mode (default, plan, full_auto)")
	f.StringVar(&flagOutputFormat, "output-format", "text", "Output format (text, json, stream-json)")
	f.BoolVar(&flagVerbose, "verbose", false, "Enable verbose logging")
	f.BoolVar(&flagFast, "fast", false, "Use fast mode (lower quality, faster)")
	f.StringVar(&flagEffort, "effort", "", "Effort level (low, medium, high)")
	f.IntVar(&flagPasses, "passes", 0, "Number of passes")
	f.BoolVar(&flagPrint, "print", false, "Non-interactive mode: read stdin, print response")
	f.StringVarP(&flagPrompt, "prompt", "p", "", "Prompt to execute directly")
	f.StringVar(&flagResume, "resume", "", "Resume a session by ID")
	f.BoolVar(&flagContinue, "continue", false, "Continue the most recent session")

	// Subcommands
	root.AddCommand(newMCPCmd())
	root.AddCommand(newAuthCmd())

	return root
}

func applyFlags(s *config.Settings, model, apiKey, baseURL string, maxTokens int,
	systemPrompt, permissionMode, outputFormat string,
	verbose, fast bool, effort string, passes int) {

	if model != "" {
		s.Model = model
	}
	if apiKey != "" {
		s.APIKey = apiKey
	}
	if baseURL != "" {
		s.BaseURL = &baseURL
	}
	if maxTokens > 0 {
		s.MaxTokens = maxTokens
	}
	if systemPrompt != "" {
		s.SystemPrompt = &systemPrompt
	}
	if permissionMode != "" {
		s.Permission.Mode = config.PermissionMode(permissionMode)
	}
	if outputFormat != "" {
		s.OutputStyle = outputFormat
	}
	if verbose {
		s.Verbose = true
	}
	if fast {
		s.FastMode = true
	}
	if effort != "" {
		s.Effort = effort
	}
	if passes > 0 {
		s.Passes = passes
	}
}

func readStdin() string {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := os.Stdin.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}

// ---------------------------------------------------------------------------
// mcp subcommand
// ---------------------------------------------------------------------------

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			if len(settings.McpServers) == 0 {
				fmt.Println("No MCP servers configured.")
				return nil
			}
			for name, srv := range settings.McpServers {
				fmt.Printf("  %s: %s %v\n", name, srv.Command, srv.Args)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "add [name] [command] [args...]",
		Short: "Add an MCP server",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			name := args[0]
			settings.McpServers[name] = config.McpServerConfig{
				Command: args[1],
				Args:    args[2:],
			}
			if err := config.SaveSettings(settings); err != nil {
				return err
			}
			fmt.Printf("Added MCP server %q\n", name)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove [name]",
		Short: "Remove an MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			name := args[0]
			if _, ok := settings.McpServers[name]; !ok {
				return fmt.Errorf("MCP server %q not found", name)
			}
			delete(settings.McpServers, name)
			if err := config.SaveSettings(settings); err != nil {
				return err
			}
			fmt.Printf("Removed MCP server %q\n", name)
			return nil
		},
	})
	return cmd
}

// ---------------------------------------------------------------------------
// auth subcommand
// ---------------------------------------------------------------------------

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			status := "not configured"
			if settings.APIKey != "" {
				status = "configured (api_key set)"
			} else if os.Getenv("ANTHROPIC_API_KEY") != "" {
				status = "configured (ANTHROPIC_API_KEY env)"
			}
			fmt.Printf("Auth status: %s\n", status)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "login",
		Short: "Configure API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Print("Enter API key: ")
			var key string
			if _, err := fmt.Scanln(&key); err != nil {
				return err
			}
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			settings.APIKey = key
			if err := config.SaveSettings(settings); err != nil {
				return err
			}
			fmt.Println("API key saved.")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Remove stored API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			settings.APIKey = ""
			if err := config.SaveSettings(settings); err != nil {
				return err
			}
			fmt.Println("API key removed.")
			return nil
		},
	})
	return cmd
}
