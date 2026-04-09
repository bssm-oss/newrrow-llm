package auth

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"

	"github.com/yourusername/newrrowllm/internal/browser"
)

const directAuthURL = "https://auth.newrrow.com/auth/authorize?domain=bssm&serviceType=CSR&apprServiceType=false&port=&pageUrl=csr-platform%2Fhome"

func Authenticate(ctx context.Context, cfg browser.Config, logger *logrus.Logger, input io.Reader) error {
	var navigateErr error
	for attempt := 0; attempt < 3; attempt++ {
		navigateErr = chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
			_, _, _, err := cdppage.Navigate(directAuthURL).Do(actionCtx)
			return err
		}))
		if navigateErr == nil {
			break
		}
		if !strings.Contains(navigateErr.Error(), "context canceled") {
			break
		}
		time.Sleep(1200 * time.Millisecond)
	}
	if navigateErr != nil {
		return fmt.Errorf("open CSR home: %w", navigateErr)
	}
	if err := attemptAutomaticLogin(ctx, logger); err != nil {
		return err
	}

	logger.Info("Waiting for authentication to complete in the connected Lightpanda browser window")
	if hasCredentials() {
		if err := waitForAutomatedAuthentication(ctx, cfg, logger); err != nil {
			return err
		}
	} else {
		if err := waitForManualConfirmation(input); err != nil {
			return err
		}
		if err := completeInvitationIfPresent(ctx, cfg.BaseURL); err != nil && !isTransientExecutionContextError(err) {
			return err
		}
		if err := waitForReusableSessionState(ctx, cfg.LoginTimeout); err != nil {
			return err
		}
	}
	if err := navigateToAuthenticatedHome(ctx, cfg.BaseURL); err != nil && !isTransientExecutionContextError(err) {
		return err
	}

	_ = chromedp.Run(ctx, chromedp.Sleep(1500*time.Millisecond))

	if err := browser.SaveSession(ctx, cfg.CookiesPath, authenticatedHomeURL(cfg.BaseURL)); err != nil {
		if !isTransientExecutionContextError(err) {
			return err
		}
		if err := browser.SaveSession(context.WithoutCancel(ctx), cfg.CookiesPath, authenticatedHomeURL(cfg.BaseURL)); err != nil {
			return err
		}
	}

	return nil
}

func hasCredentials() bool {
	return strings.TrimSpace(os.Getenv("NEWRROW_EMAIL")) != "" && strings.TrimSpace(os.Getenv("NEWRROW_PASSWORD")) != ""
}

func waitForAutomatedAuthentication(ctx context.Context, cfg browser.Config, logger *logrus.Logger) error {
	deadline := time.Now().Add(cfg.LoginTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := attemptAutomaticLogin(ctx, logger); err != nil && !isTransientExecutionContextError(err) {
			return err
		}
		if err := completeInvitationIfPresent(ctx, cfg.BaseURL); err != nil && !isTransientExecutionContextError(err) {
			return err
		}
		if err := waitForReusableSessionState(ctx, 2*time.Second); err == nil {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("login did not produce reusable NEWRROW session state before timeout")
}

func waitForManualConfirmation(input io.Reader) error {
	if input == nil {
		return fmt.Errorf("manual confirmation required but no stdin is available")
	}
	fmt.Fprintln(os.Stderr, "Press Enter after you finish logging in inside the Lightpanda browser window.")
	reader := bufio.NewReader(input)
	_, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read login confirmation: %w", err)
	}
	return nil
}

func attemptAutomaticLogin(ctx context.Context, logger *logrus.Logger) error {
	email := strings.TrimSpace(os.Getenv("NEWRROW_EMAIL"))
	password := strings.TrimSpace(os.Getenv("NEWRROW_PASSWORD"))
	if email == "" || password == "" {
		return nil
	}
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return nil
	}
	if !strings.Contains(currentURL, "auth.inhrplus.com/auth/universal-login/login") {
		return nil
	}
	logger.Info("Detected credentials in environment; attempting automatic login before falling back to manual completion")
	script := fmt.Sprintf(`(() => {
		const id = document.querySelector('#accountId');
		const pw = document.querySelector('#accountPassword');
		const submit = document.querySelector('#loginSubmit');
		if (!id || !pw || !submit) return 'missing-form';
		id.value = %q;
		id.dispatchEvent(new Event('input', { bubbles: true }));
		id.dispatchEvent(new Event('change', { bubbles: true }));
		pw.value = %q;
		pw.dispatchEvent(new Event('input', { bubbles: true }));
		pw.dispatchEvent(new Event('change', { bubbles: true }));
		submit.click();
		return 'ok';
	})()`, email, password)
	var result string
	if err := chromedp.Run(ctx,
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(script, &result),
	); err != nil {
		return fmt.Errorf("attempt automatic login submit: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("attempt automatic login submit: %s", result)
	}
	return nil
}
func loggerDiscard() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(os.Stderr)
	return logger
}

func completeInvitationIfPresent(ctx context.Context, baseURL string) error {
	deadline := time.Now().Add(15 * time.Second)
	var sawInvitation bool
	for time.Now().Before(deadline) {
		var currentURL string
		if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
			if isTransientExecutionContextError(err) {
				time.Sleep(700 * time.Millisecond)
				continue
			}
			return fmt.Errorf("check current URL for invitation: %w", err)
		}
		if !strings.Contains(currentURL, "/csr-platform/invitation") {
			if sawInvitation {
				return nil
			}
			return nil
		}
		sawInvitation = true
		if err := chromedp.Run(ctx,
			chromedp.Navigate(baseURL),
			chromedp.Sleep(1500*time.Millisecond),
		); err == nil {
			return nil
		}
		time.Sleep(700 * time.Millisecond)
	}
	return nil
}

func isTransientExecutionContextError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Cannot find default execution context") || strings.Contains(msg, "context canceled") || strings.Contains(msg, "invalid context")
}

func currentURL(ctx context.Context) (string, error) {
	var loc string
	if err := chromedp.Run(ctx, chromedp.Location(&loc)); err == nil {
		return loc, nil
	}
	frameTree, err := cdppage.GetFrameTree().Do(ctx)
	if err != nil {
		return "", fmt.Errorf("get frame tree: %w", err)
	}
	if frameTree == nil || frameTree.Frame == nil {
		return "", nil
	}
	return frameTree.Frame.URL, nil
}

func waitForReusableSessionState(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cookies, err := network.GetCookies().Do(ctx)
		if err == nil {
			hasAccess := false
			hasRefresh := false
			for _, cookie := range cookies {
				switch cookie.Name {
				case "csrAccessToken":
					hasAccess = true
				case "csrRefreshToken":
					hasRefresh = true
				}
			}
			if hasAccess && hasRefresh {
				return nil
			}
		}
		if err != nil && !isTransientExecutionContextError(err) {
			return fmt.Errorf("wait for reusable session state cookies: %w", err)
		}

		currentURL, err := currentURL(ctx)
		if err == nil {
			lower := strings.ToLower(currentURL)
			if strings.HasPrefix(lower, "https://bssm.newrrow.com/csr-platform/home") {
				return nil
			}
		}
		if err != nil && !isTransientExecutionContextError(err) {
			return fmt.Errorf("wait for reusable session state: %w", err)
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return fmt.Errorf("login did not produce reusable NEWRROW session state before timeout")
}

func navigateToAuthenticatedHome(ctx context.Context, baseURL string) error {
	homeURL := authenticatedHomeURL(baseURL)
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		_, _, _, err := cdppage.Navigate(homeURL).Do(actionCtx)
		return err
	})); err != nil {
		return fmt.Errorf("navigate to authenticated home before save: %w", err)
	}
	if err := completeInvitationIfPresent(ctx, baseURL); err != nil && !isTransientExecutionContextError(err) {
		return err
	}
	return nil
}

func authenticatedHomeURL(baseURL string) string {
	if strings.Contains(baseURL, "?") {
		return baseURL + "&login=true"
	}
	return baseURL + "?login=true"
}
