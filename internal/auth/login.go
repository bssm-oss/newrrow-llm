package auth

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"

	"github.com/bssm-oss/newrrow-llm/internal/browser"
)

const directAuthURL = "https://auth.newrrow.com/auth/authorize?domain=bssm&serviceType=CSR&apprServiceType=false&port=&pageUrl=csr-platform%2Fhome"

func directHRPAuthURL() string {
	return "https://auth.newrrow.com/auth/authorize/hrp-auth?current=" + url.QueryEscape(directAuthURL)
}

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
	if navigateErr != nil && !isTransientExecutionContextError(navigateErr) {
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
		if err := waitForReusableSessionState(ctx, cfg.LoginTimeout, cfg.BaseURL, true); err != nil {
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

func EnsureReusableSession(ctx context.Context, cfg browser.Config) error {
	if err := completeInvitationIfPresent(ctx, cfg.BaseURL); err != nil && !isTransientExecutionContextError(err) {
		return err
	}
	if err := navigateToAuthenticatedHome(ctx, cfg.BaseURL); err != nil && !isTransientExecutionContextError(err) {
		return err
	}
	return waitForReusableSessionState(ctx, cfg.LoginTimeout, cfg.BaseURL, true)
}

func DirectAuthenticate(cfg browser.Config) error {
	email := strings.TrimSpace(os.Getenv("NEWRROW_EMAIL"))
	password := strings.TrimSpace(os.Getenv("NEWRROW_PASSWORD"))
	if email == "" || password == "" {
		return fmt.Errorf("NEWRROW_EMAIL and NEWRROW_PASSWORD are required when no CDP browser is available")
	}
	jar, err := newRecordingJar()
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Timeout: cfg.LoginTimeout}

	resp, err := client.Get(directHRPAuthURL())
	if err != nil {
		return fmt.Errorf("open auth login page: %w", err)
	}
	resp.Body.Close()
	loginURL := resp.Request.URL
	qs := loginURL.Query()
	domain := "bssm"
	clientName := "CSR"
	returnTo := qs.Get("returnTo")
	if returnTo == "" {
		return fmt.Errorf("could not determine login parameters from auth page")
	}

	loginReqBody := strings.NewReader(fmt.Sprintf(`{"accountId":%q,"accountPassword":%q,"client":%q,"domain":%q}`, email, password, clientName, domain))
	loginReq, err := http.NewRequest(http.MethodPost, "https://auth.inhrplus.com/api/v1/universal-login/login?domain="+url.QueryEscape(domain), loginReqBody)
	if err != nil {
		return err
	}
	loginReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	loginReq.Header.Set("Accept", "application/json, text/plain, */*")
	loginReq.Header.Set("Referer", loginURL.String())
	loginResp, err := client.Do(loginReq)
	if err != nil {
		return fmt.Errorf("submit login api: %w", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode < 200 || loginResp.StatusCode >= 300 {
		return fmt.Errorf("submit login api: status %s", loginResp.Status)
	}
	var loginData struct {
		NextType  string `json:"nextType"`
		AuthState string `json:"authState"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginData); err != nil {
		return fmt.Errorf("decode login api response: %w", err)
	}
	if loginData.NextType != "DONE" || loginData.AuthState == "" {
		return fmt.Errorf("unsupported login nextType: %s", loginData.NextType)
	}

	doneReq, err := http.NewRequest(http.MethodPost, "https://auth.inhrplus.com/api/v1/universal-login-sessions/universal-login?domain="+url.QueryEscape(domain)+"&authState="+url.QueryEscape(loginData.AuthState), nil)
	if err != nil {
		return err
	}
	doneReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	doneReq.Header.Set("Referer", loginURL.String())
	doneResp, err := client.Do(doneReq)
	if err != nil {
		return fmt.Errorf("complete login session: %w", err)
	}
	doneResp.Body.Close()

	doneURL, err := url.Parse("https://auth.inhrplus.com" + returnTo + "&authState=" + url.QueryEscape(loginData.AuthState))
	if err != nil {
		return fmt.Errorf("parse done url: %w", err)
	}
	innerReturnTo := doneURL.Query().Get("returnTo")
	if innerReturnTo == "" {
		return fmt.Errorf("done page did not include nested returnTo")
	}
	callbackURL := "https://auth.inhrplus.com" + innerReturnTo
	callbackResp, err := client.Get(callbackURL)
	if err != nil {
		return fmt.Errorf("follow auth callback chain: %w", err)
	}
	callbackResp.Body.Close()

	_, _ = client.Get(authenticatedHomeURL(cfg.BaseURL))

	persisted := jar.PersistedCookies()
	if len(persisted) == 0 {
		return fmt.Errorf("direct auth produced no cookies")
	}
	session := browser.PersistedSession{Cookies: persisted, Origins: []browser.PersistedOrigin{{Origin: "https://bssm.newrrow.com"}}}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.CookiesPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cfg.CookiesPath, data, 0o600); err != nil {
		return err
	}
	return nil
}

type recordingJar struct {
	base http.CookieJar
	mu   sync.Mutex
	seen map[string]*http.Cookie
}

func newRecordingJar() (*recordingJar, error) {
	base, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &recordingJar{base: base, seen: map[string]*http.Cookie{}}, nil
}

func (j *recordingJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.base.SetCookies(u, cookies)
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cookies {
		copyCookie := *c
		if copyCookie.Domain == "" {
			copyCookie.Domain = u.Hostname()
		}
		if copyCookie.Path == "" {
			copyCookie.Path = "/"
		}
		key := copyCookie.Domain + "|" + copyCookie.Path + "|" + copyCookie.Name
		j.seen[key] = &copyCookie
	}
}

func (j *recordingJar) Cookies(u *url.URL) []*http.Cookie {
	return j.base.Cookies(u)
}

func (j *recordingJar) PersistedCookies() []browser.PersistedCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]browser.PersistedCookie, 0, len(j.seen))
	for _, c := range j.seen {
		item := browser.PersistedCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			HTTPOnly: c.HttpOnly,
			Secure:   c.Secure,
		}
		if !c.Expires.IsZero() {
			expires := float64(c.Expires.Unix())
			item.Expires = &expires
		}
		out = append(out, item)
	}
	return out
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
		if err := waitForReusableSessionState(ctx, 2*time.Second, cfg.BaseURL, false); err == nil {
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
	if !strings.Contains(currentURL, "auth.inhrplus.com") {
		return nil
	}
	logger.Info("Detected credentials in environment; attempting automatic login before falling back to manual completion")
	script := fmt.Sprintf(`(async () => {
		if (typeof requestLoginApi !== 'function' || typeof createPageUrlFromLoginByNextType !== 'function' || typeof getUniversalLoginRequiredParamsFromQs !== 'function') {
			return 'missing-login-helpers';
		}
		const { domain, client, returnTo, serviceType } = getUniversalLoginRequiredParamsFromQs();
		if (!domain || !client || !returnTo || !serviceType) {
			return 'missing-login-params';
		}
		const data = await requestLoginApi(domain, %q, %q, client);
		const next = createPageUrlFromLoginByNextType(data.nextType, domain, data.authState, serviceType, data.accountId, returnTo);
		window.location.replace(next);
		return 'ok';
	})()`, email, password)
	var result string
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		if err := chromedp.Sleep(500 * time.Millisecond).Do(actionCtx); err != nil {
			return err
		}
		res, exp, err := cdpruntime.Evaluate(script).WithAwaitPromise(true).WithReturnByValue(true).Do(actionCtx)
		if err != nil {
			return err
		}
		if exp != nil {
			return fmt.Errorf("%s", exp.Text)
		}
		if res == nil {
			return fmt.Errorf("runtime evaluate returned no result")
		}
		return json.Unmarshal(res.Value, &result)
	})); err != nil {
		return fmt.Errorf("attempt automatic login submit: %w", err)
	}
	if result == "missing-form" {
		return nil
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

func waitForReusableSessionState(ctx context.Context, timeout time.Duration, baseURL string, navigateHome bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cookies, err := network.GetCookies().WithUrls([]string{
			authenticatedHomeURL(baseURL),
			baseURL,
			"https://auth.newrrow.com/",
			"https://auth.inhrplus.com/",
		}).Do(ctx)
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

		if navigateHome {
			_ = navigateToAuthenticatedHome(ctx, baseURL)
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
