package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yourusername/newrrowllm/internal/browser"
)

var (
	rootCmd = &cobra.Command{
		Use:   "newrrowllm",
		Short: "CLI for the BSSM NEWRROW CSR agent",
		Long:  "newrrowllm authenticates to the BSSM NEWRROW CSR platform through a pre-started Lightpanda CDP endpoint and sends chat messages to the built-in agent.",
	}

	appViper = viper.New()
	appCfg   browser.Config
	logger   = logrus.New()
)

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().String("config", "", "config file path")
	rootCmd.PersistentFlags().Bool("debug", false, "enable verbose chromedp/log output")
	rootCmd.PersistentFlags().String("cdp-endpoint", "http://127.0.0.1:9222", "Lightpanda CDP endpoint")
	rootCmd.PersistentFlags().String("base-url", browser.DefaultBaseURL, "NEWRROW CSR platform home URL")
	rootCmd.PersistentFlags().Duration("timeout", 60*time.Second, "timeout for individual chromedp operations")
	rootCmd.PersistentFlags().Duration("login-timeout", 5*time.Minute, "timeout for manual authentication completion")
	rootCmd.PersistentFlags().String("cookies-path", defaultCookiesPath(), "path to persisted cookies JSON")
	rootCmd.PersistentFlags().String("chat-input-selector", browser.DefaultInputSelector, "fallback CSS selectors for the chat input")
	rootCmd.PersistentFlags().String("chat-send-selector", browser.DefaultSendSelector, "fallback CSS selectors for the send button")
	rootCmd.PersistentFlags().String("chat-reply-selector", browser.DefaultReplySelector, "fallback CSS selectors for assistant replies")
	rootCmd.PersistentFlags().String("chat-ready-selector", browser.DefaultReadySelector, "CSS selectors that indicate the chat UI is ready")
	rootCmd.PersistentFlags().String("chat-launcher-selector", browser.DefaultLauncherSelector, "CSS selectors for opening the agent UI")

	mustBindFlag("config")
	mustBindFlag("debug")
	mustBindFlag("cdp-endpoint")
	mustBindFlag("base-url")
	mustBindFlag("timeout")
	mustBindFlag("login-timeout")
	mustBindFlag("cookies-path")
	mustBindFlag("chat-input-selector")
	mustBindFlag("chat-send-selector")
	mustBindFlag("chat-reply-selector")
	mustBindFlag("chat-ready-selector")
	mustBindFlag("chat-launcher-selector")
}

func mustBindFlag(name string) {
	if err := appViper.BindPFlag(name, rootCmd.PersistentFlags().Lookup(name)); err != nil {
		panic(err)
	}
}

func initConfig() {
	logger.SetOutput(os.Stderr)
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, ForceColors: true})
	logger.SetLevel(logrus.InfoLevel)
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, ForceColors: true})
	logrus.SetLevel(logrus.InfoLevel)

	appViper.SetEnvPrefix("NEWRROWLLM")
	appViper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	appViper.AutomaticEnv()

	_ = appViper.BindEnv("chat-input-selector", "NEWRROWLLM_AGENT_INPUT_SELECTOR")
	_ = appViper.BindEnv("chat-send-selector", "NEWRROWLLM_AGENT_SEND_SELECTOR")
	_ = appViper.BindEnv("chat-ready-selector", "NEWRROWLLM_AGENT_READY_SELECTOR")
	_ = appViper.BindEnv("chat-launcher-selector", "NEWRROWLLM_AGENT_LAUNCHER_SELECTOR")
	_ = appViper.BindEnv("chat-reply-selector", "NEWRROWLLM_AGENT_REPLY_SELECTOR", "NEWRROWM_AGENT_REPLY_SELECTOR")

	configPath := appViper.GetString("config")
	if configPath != "" {
		appViper.SetConfigFile(configPath)
	} else {
		appViper.SetConfigName("newrrowllm")
		appViper.SetConfigType("yaml")
		appViper.AddConfigPath(".")
		if home, err := os.UserHomeDir(); err == nil {
			appViper.AddConfigPath(filepath.Join(home, ".newrrowllm"))
		}
	}

	if err := appViper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok && configPath != "" {
			cobra.CheckErr(fmt.Errorf("read config: %w", err))
		}
	}

	appCfg = browser.Config{
		BaseURL:           appViper.GetString("base-url"),
		CDPEndpoint:       appViper.GetString("cdp-endpoint"),
		CookiesPath:       appViper.GetString("cookies-path"),
		ActionTimeout:     appViper.GetDuration("timeout"),
		LoginTimeout:      appViper.GetDuration("login-timeout"),
		InputSelectors:    browser.SplitSelectors(appViper.GetString("chat-input-selector")),
		SendSelectors:     browser.SplitSelectors(appViper.GetString("chat-send-selector")),
		ReplySelectors:    browser.SplitSelectors(appViper.GetString("chat-reply-selector")),
		ReadySelectors:    browser.SplitSelectors(appViper.GetString("chat-ready-selector")),
		LauncherSelectors: browser.SplitSelectors(appViper.GetString("chat-launcher-selector")),
		Debug:             appViper.GetBool("debug"),
	}

	if appCfg.BaseURL == "" {
		appCfg.BaseURL = browser.DefaultBaseURL
	}
	if appCfg.CDPEndpoint == "" {
		appCfg.CDPEndpoint = "http://127.0.0.1:9222"
	}
	if appCfg.CookiesPath == "" {
		appCfg.CookiesPath = defaultCookiesPath()
	}
	appCfg.CookiesPath = expandHomePath(appCfg.CookiesPath)
	if appCfg.ActionTimeout <= 0 {
		appCfg.ActionTimeout = 60 * time.Second
	}
	if appCfg.LoginTimeout <= 0 {
		appCfg.LoginTimeout = 5 * time.Minute
	}
	if len(appCfg.InputSelectors) == 0 {
		appCfg.InputSelectors = browser.SplitSelectors(browser.DefaultInputSelector)
	}
	if len(appCfg.SendSelectors) == 0 {
		appCfg.SendSelectors = browser.SplitSelectors(browser.DefaultSendSelector)
	}
	if len(appCfg.ReplySelectors) == 0 {
		appCfg.ReplySelectors = browser.SplitSelectors(browser.DefaultReplySelector)
	}
	if len(appCfg.ReadySelectors) == 0 {
		appCfg.ReadySelectors = browser.SplitSelectors(browser.DefaultReadySelector)
	}
	if len(appCfg.LauncherSelectors) == 0 {
		appCfg.LauncherSelectors = browser.SplitSelectors(browser.DefaultLauncherSelector)
	}

	if appCfg.Debug {
		logger.SetLevel(logrus.DebugLevel)
		logrus.SetLevel(logrus.DebugLevel)
	}
}

func defaultCookiesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".newrrowllm/cookies.json"
	}
	return filepath.Join(home, ".newrrowllm", "cookies.json")
}

func expandHomePath(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}
