package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/yourusername/newrrowllm/internal/agent"
	"github.com/yourusername/newrrowllm/internal/browser"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check whether the persisted session is still valid",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := os.Stat(appCfg.CookiesPath); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "⚠️ Session expired. Run 'newrrowllm auth'.")
				return nil
			}
			return fmt.Errorf("stat cookies: %w", err)
		}

		ctx, cancel, err := browser.ConnectToLightpanda(cmd.Context(), appCfg)
		if err != nil {
			return err
		}
		defer cancel()

		statusCtx, statusCancel := context.WithTimeout(ctx, appCfg.ActionTimeout)
		defer statusCancel()

		valid, currentURL, err := agent.CheckStatus(statusCtx, appCfg)
		if err != nil {
			return err
		}

		if valid {
			fmt.Fprintf(cmd.OutOrStdout(), "✅ Session valid (%s)\n", currentURL)
			return nil
		}

		fmt.Fprintln(cmd.OutOrStdout(), "⚠️ Session expired. Run 'newrrowllm auth'.")
		return nil
	},
}
