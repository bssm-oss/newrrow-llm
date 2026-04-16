package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bssm-oss/newrrow-llm/internal/agent"
	internalauth "github.com/bssm-oss/newrrow-llm/internal/auth"
	"github.com/bssm-oss/newrrow-llm/internal/browser"
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

		_, err := ensureChatSession(cmd.Context(), cmd)
		if err != nil {
			return err
		}

		chatBrowserCtx, chatBrowserCancel, err := browser.ConnectToLightpanda(cmd.Context(), appCfg)
		if err != nil {
			reply, httpErr := agent.SendChatMessageHTTPOnly(appCfg, message)
			if httpErr != nil {
				return httpErr
			}
			fmt.Fprintf(cmd.OutOrStdout(), "🤖 Agent: %s\n", reply)
			return nil
		}
		defer chatBrowserCancel()

		chatCtx, chatCancel := context.WithTimeout(chatBrowserCtx, appCfg.LoginTimeout+appCfg.ActionTimeout)
		defer chatCancel()

		reply, err := agent.SendChatMessage(chatCtx, appCfg, message, logger)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "🤖 Agent: %s\n", reply)
		return nil
	},
}

func ensureChatSession(ctx context.Context, cmd *cobra.Command) (bool, error) {
	if _, err := os.Stat(appCfg.CookiesPath); err == nil {
		statusBrowserCtx, statusBrowserCancel, connectErr := browser.ConnectToLightpanda(ctx, appCfg)
		if connectErr != nil {
			goto establish
		}
		defer statusBrowserCancel()

		statusCtx, statusCancel := context.WithTimeout(statusBrowserCtx, appCfg.LoginTimeout+appCfg.ActionTimeout)
		defer statusCancel()

		valid, _, statusErr := agent.CheckStatus(statusCtx, appCfg)
		if statusErr != nil {
			return false, fmt.Errorf("check saved session: %w", statusErr)
		}
		if valid {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat cookies: %w", err)
	}

establish:
	fmt.Fprintln(cmd.ErrOrStderr(), "🔐 Establishing session for chat...")
	if _, _, err := browser.ConnectToLightpanda(ctx, appCfg); err != nil {
		if err := internalauth.DirectAuthenticate(appCfg); err != nil {
			return false, err
		}
		return false, nil
	}
	var authErr error
	for attempt := 0; attempt < 3; attempt++ {
		authErr = runAuthSubcommand(ctx, cmd)
		if authErr == nil {
			postAuthBrowserCtx, postAuthBrowserCancel, connectErr := browser.ConnectToLightpanda(ctx, appCfg)
			if connectErr != nil {
				return false, connectErr
			}
			defer postAuthBrowserCancel()

			postAuthCtx, postAuthCancel := context.WithTimeout(postAuthBrowserCtx, appCfg.LoginTimeout+appCfg.ActionTimeout)
			defer postAuthCancel()

			if err := internalauth.EnsureReusableSession(postAuthCtx, appCfg); err != nil {
				authErr = fmt.Errorf("post-auth session validation: %w", err)
			} else if err := browser.SaveSession(postAuthCtx, appCfg.CookiesPath, appCfg.BaseURL+"?login=true"); err != nil {
				authErr = fmt.Errorf("post-auth session save: %w", err)
			} else {
				return true, nil
			}
		}
		if authErr == nil {
			return true, nil
		}
		if attempt < 2 {
			fmt.Fprintln(cmd.ErrOrStderr(), "↻ Retrying session bootstrap after auth subprocess failure...")
			time.Sleep(1200 * time.Millisecond)
		}
	}
	return false, fmt.Errorf("establish chat session: %w", authErr)
}

func runAuthSubcommand(ctx context.Context, cmd *cobra.Command) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	args := []string{"auth"}
	if appCfg.Debug {
		args = append(args, "--debug")
	}

	authTimeout := appCfg.LoginTimeout + appCfg.ActionTimeout
	if os.Getenv("NEWRROW_EMAIL") != "" && os.Getenv("NEWRROW_PASSWORD") != "" {
		authTimeout = 45 * time.Second
	}

	authCtx, authCancel := context.WithTimeout(ctx, authTimeout)
	defer authCancel()

	authCmd := exec.CommandContext(authCtx, exePath, args...)
	authCmd.Env = append(os.Environ(),
		"NEWRROWLLM_BASE_URL="+appCfg.BaseURL,
		"NEWRROWLLM_CDP_ENDPOINT="+appCfg.CDPEndpoint,
		"NEWRROWLLM_COOKIES_PATH="+appCfg.CookiesPath,
		"NEWRROWLLM_TIMEOUT="+appCfg.ActionTimeout.String(),
		"NEWRROWLLM_LOGIN_TIMEOUT="+appCfg.LoginTimeout.String(),
		"NEWRROWLLM_AGENT_INPUT_SELECTOR="+strings.Join(appCfg.InputSelectors, ", "),
		"NEWRROWLLM_AGENT_SEND_SELECTOR="+strings.Join(appCfg.SendSelectors, ", "),
		"NEWRROWLLM_AGENT_REPLY_SELECTOR="+strings.Join(appCfg.ReplySelectors, ", "),
		"NEWRROWLLM_AGENT_READY_SELECTOR="+strings.Join(appCfg.ReadySelectors, ", "),
		"NEWRROWLLM_AGENT_LAUNCHER_SELECTOR="+strings.Join(appCfg.LauncherSelectors, ", "),
	)
	authCmd.Stdin = cmd.InOrStdin()
	authCmd.Stdout = cmd.OutOrStdout()
	authCmd.Stderr = cmd.ErrOrStderr()
	if err := authCmd.Run(); err != nil {
		if authCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("auth subprocess timed out")
		}
		return err
	}
	return nil
}
