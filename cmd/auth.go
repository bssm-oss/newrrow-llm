package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yourusername/newrrowllm/internal/auth"
	"github.com/yourusername/newrrowllm/internal/browser"
)

func init() {
	rootCmd.AddCommand(authCmd)
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Open the CSR page and persist an authenticated cookie session",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "🌐 Opening browser for login. Please log in to BSSM NEWRROW CS...")

		ctx, cancel, err := browser.ConnectToLightpanda(cmd.Context(), appCfg)
		if err != nil {
			return err
		}
		defer cancel()

		authCtx, authCancel := context.WithTimeout(ctx, appCfg.LoginTimeout+appCfg.ActionTimeout)
		defer authCancel()

		if err := auth.Authenticate(authCtx, appCfg, logger, cmd.InOrStdin()); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "✅ Authentication successful. Cookies saved.")
		return nil
	},
}
