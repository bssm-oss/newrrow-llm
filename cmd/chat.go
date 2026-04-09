package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yourusername/newrrowllm/internal/agent"
	"github.com/yourusername/newrrowllm/internal/browser"
)

func init() {
	rootCmd.AddCommand(chatCmd)
}

var chatCmd = &cobra.Command{
	Use:   "chat \"message\"",
	Short: "Send a message to the CSR agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		message := strings.TrimSpace(args[0])
		if message == "" {
			return fmt.Errorf("message must not be empty")
		}
		if _, err := os.Stat(appCfg.CookiesPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("cookies not found. Run 'newrrowllm auth' first")
			}
			return fmt.Errorf("stat cookies: %w", err)
		}

		ctx, cancel, err := browser.ConnectToLightpanda(cmd.Context(), appCfg)
		if err != nil {
			return err
		}
		defer cancel()

		chatCtx, chatCancel := context.WithTimeout(ctx, appCfg.LoginTimeout+appCfg.ActionTimeout)
		defer chatCancel()

		reply, err := agent.SendChatMessage(chatCtx, appCfg, message, logger)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "🤖 Agent: %s\n", reply)
		return nil
	},
}
