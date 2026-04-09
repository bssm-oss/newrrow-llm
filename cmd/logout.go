package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(logoutCmd)
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Delete the persisted cookie session",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := os.Remove(appCfg.CookiesPath); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "🗑️ Cookies already removed.")
				return nil
			}
			return fmt.Errorf("delete cookies: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "🗑️ Cookies deleted.")
		return nil
	},
}
