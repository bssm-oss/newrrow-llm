package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"
)

const (
	DefaultBaseURL          = "https://bssm.newrrow.com/csr-platform/home"
	DefaultLauncherSelector = "button.NewrrowAgentFloatingButton-module__container--K6YjH, button[class*=\"NewrrowAgentFloatingButton-module__container\"]"
	DefaultInputSelector    = "textarea.NewrrowAgentPromptBar-module__input--tDIzY, textarea[class*=\"NewrrowAgentPromptBar-module__input\"], textarea[placeholder=\"궁금한 게 있으신가요? 얼마든지 물어보세요!\"], textarea, [contenteditable=\"true\"], .chat-input"
	DefaultSendSelector     = "button.NewrrowAgentPromptBar-module__sendMessageButton--4UJJd, button[class*=\"NewrrowAgentPromptBar-module__sendMessageButton\"], button[type=\"submit\"], [aria-label=\"Send\"], button:has(svg[data-icon=\"send\"]), button:has(svg)"
	DefaultReplySelector    = "li.AgentMessage-module__container--zbDAj, li[class*=\"AgentMessage-module__container\"], [class*=\"AgentMessage-module__messageSection\"], [data-message-author-role=\"assistant\"], .agent-message, .chat-message, [class*=\"assistant\"], [class*=\"agent\"], [class*=\"bot\"], [class*=\"response\"]"
	DefaultReadySelector    = "textarea.NewrrowAgentPromptBar-module__input--tDIzY, textarea[class*=\"NewrrowAgentPromptBar-module__input\"], textarea[placeholder=\"궁금한 게 있으신가요? 얼마든지 물어보세요!\"], button.NewrrowAgentFloatingButton-module__container--K6YjH, button[class*=\"NewrrowAgentFloatingButton-module__container\"], [data-testid=\"chat-input\"]"
)

type Config struct {
	BaseURL           string
	CDPEndpoint       string
	CookiesPath       string
	ActionTimeout     time.Duration
	LoginTimeout      time.Duration
	InputSelectors    []string
	SendSelectors     []string
	ReplySelectors    []string
	ReadySelectors    []string
	LauncherSelectors []string
	Debug             bool
}

type PersistedCookie struct {
	Name     string                 `json:"name"`
	Value    string                 `json:"value"`
	Domain   string                 `json:"domain"`
	Path     string                 `json:"path"`
	Expires  *float64               `json:"expires,omitempty"`
	HTTPOnly bool                   `json:"httpOnly,omitempty"`
	Secure   bool                   `json:"secure,omitempty"`
	SameSite network.CookieSameSite `json:"sameSite,omitempty"`
}

type PersistedStorageItem struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type PersistedOrigin struct {
	Origin       string                 `json:"origin"`
	LocalStorage []PersistedStorageItem `json:"localStorage,omitempty"`
}

type PersistedSession struct {
	Cookies []PersistedCookie `json:"cookies"`
	Origins []PersistedOrigin `json:"origins,omitempty"`
}

func SplitSelectors(raw string) []string {
	parts := strings.Split(raw, ",")
	selectors := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			selectors = append(selectors, trimmed)
		}
	}
	return selectors
}

func FirstVisibleSelectorScript(selectors []string) (string, error) {
	encoded, err := json.Marshal(selectors)
	if err != nil {
		return "", fmt.Errorf("marshal selectors: %w", err)
	}
	return fmt.Sprintf(`(() => {
		const selectors = %s;
		const isVisible = (el) => !!el && !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
		for (const selector of selectors) {
			try {
				const nodes = document.querySelectorAll(selector);
				for (const node of nodes) {
					if (isVisible(node)) return selector;
				}
			} catch (_) {}
		}
		return "";
	})()`, string(encoded)), nil
}

func CountVisibleMatchesScript(selectors []string) (string, error) {
	encoded, err := json.Marshal(selectors)
	if err != nil {
		return "", fmt.Errorf("marshal selectors: %w", err)
	}
	return fmt.Sprintf(`(() => {
		const selectors = %s;
		const isVisible = (el) => !!el && !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
		const seen = new Set();
		let count = 0;
		for (const selector of selectors) {
			try {
				const nodes = document.querySelectorAll(selector);
				for (const node of nodes) {
					if (!isVisible(node)) continue;
					if (seen.has(node)) continue;
					seen.add(node);
					count += 1;
				}
			} catch (_) {}
		}
		return count;
	})()`, string(encoded)), nil
}

func LastVisibleTextScript(selectors []string) (string, error) {
	encoded, err := json.Marshal(selectors)
	if err != nil {
		return "", fmt.Errorf("marshal selectors: %w", err)
	}
	return fmt.Sprintf(`(() => {
		const selectors = %s;
		const isVisible = (el) => !!el && !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
		const nodes = [];
		const seen = new Set();
		for (const selector of selectors) {
			try {
				for (const node of document.querySelectorAll(selector)) {
					if (!isVisible(node)) continue;
					if (seen.has(node)) continue;
					seen.add(node);
					nodes.push(node);
				}
			} catch (_) {}
		}
		if (!nodes.length) return "";
		const last = nodes[nodes.length - 1];
		return (last.innerText || last.textContent || "").trim();
	})()`, string(encoded)), nil
}

func ConnectToLightpanda(parent context.Context, cfg Config) (context.Context, context.CancelFunc, error) {
	wsURL, err := resolveWebSocketDebuggerURL(parent, cfg.CDPEndpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("Lightpanda CDP not running. Start it with: ./lightpanda --host 127.0.0.1 --port 9222 (%w)", err)
	}

	allocatorOptions := []chromedp.RemoteAllocatorOption{chromedp.NoModifyURL}

	allocatorCtx, allocatorCancel := chromedp.NewRemoteAllocator(parent, wsURL, allocatorOptions...)
	ctxOptions := []chromedp.ContextOption{}
	if cfg.Debug {
		ctxOptions = append(ctxOptions,
			chromedp.WithDebugf(logrus.StandardLogger().Debugf),
			chromedp.WithLogf(logrus.StandardLogger().Infof),
		)
	}
	ctx, ctxCancel := chromedp.NewContext(allocatorCtx, ctxOptions...)

	cancel := func() {
		ctxCancel()
		allocatorCancel()
	}

	if err := chromedp.Run(ctx); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("initialize remote chromedp context: %w", err)
	}

	return ctx, cancel, nil
}

func SaveCookies(ctx context.Context, path string) error {
	return saveSession(ctx, path, "")
}

func SaveSession(ctx context.Context, path string, originURL string) error {
	return saveSession(ctx, path, originURL)
}

func saveSession(ctx context.Context, path string, originURL string) error {
	var cookies []*network.Cookie
	currentOrigin := originURL
	var localStorage []PersistedStorageItem
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		var runErr error
		cookies, runErr = network.GetCookies().WithUrls([]string{
			"https://bssm.newrrow.com/csr-platform/home",
			"https://bssm.newrrow.com/csr-platform/home?login=true",
			"https://auth.newrrow.com/",
			"https://auth.inhrplus.com/",
		}).Do(actionCtx)
		if runErr != nil {
			return runErr
		}
		return nil
	}))
	if err != nil {
		return fmt.Errorf("get cookies: %w", err)
	}

	if currentOrigin != "" {
		_ = chromedp.Run(ctx,
			chromedp.Navigate(currentOrigin),
			chromedp.Sleep(2*time.Second),
			chromedp.Evaluate(`(() => Object.entries(localStorage).map(([name, value]) => ({ name, value })))()`, &localStorage),
		)
	}

	persisted := make([]PersistedCookie, 0, len(cookies))
	for _, cookie := range cookies {
		item := PersistedCookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			HTTPOnly: cookie.HTTPOnly,
			Secure:   cookie.Secure,
			SameSite: cookie.SameSite,
		}
		expires := float64(cookie.Expires)
		if expires > 0 {
			item.Expires = &expires
		}
		persisted = append(persisted, item)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cookies directory: %w", err)
	}

	session := PersistedSession{Cookies: persisted}
	if currentOrigin != "" {
		if parsed, parseErr := url.Parse(currentOrigin); parseErr == nil {
			session.Origins = append(session.Origins, PersistedOrigin{Origin: parsed.Scheme + "://" + parsed.Host, LocalStorage: localStorage})
		}
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cookies: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write cookies: %w", err)
	}

	return nil
}

func LoadCookies(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read cookies: %w", err)
	}

	var session PersistedSession
	if err := json.Unmarshal(data, &session); err != nil || session.Cookies == nil {
		var legacy []PersistedCookie
		if legacyErr := json.Unmarshal(data, &legacy); legacyErr != nil {
			if err != nil {
				return fmt.Errorf("unmarshal cookies: %w", err)
			}
			return fmt.Errorf("unmarshal cookies: %w", legacyErr)
		}
		session = PersistedSession{Cookies: legacy}
	}

	params := make([]*network.CookieParam, 0, len(session.Cookies))
	for _, cookie := range session.Cookies {
		param := &network.CookieParam{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			HTTPOnly: cookie.HTTPOnly,
			Secure:   cookie.Secure,
			SameSite: cookie.SameSite,
		}
		if cookie.Expires != nil {
			wholeSeconds := int64(*cookie.Expires)
			nanos := int64((*cookie.Expires - float64(wholeSeconds)) * float64(time.Second))
			expires := cdp.TimeSinceEpoch(time.Unix(wholeSeconds, nanos).UTC())
			param.Expires = &expires
		}
		params = append(params, param)
	}

	if len(params) == 0 {
		if err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
			if err := storage.ClearCookies().Do(actionCtx); err != nil {
				return fmt.Errorf("clear existing browser cookies: %w", err)
			}
			return nil
		})); err != nil {
			return err
		}
		return restoreOrigins(ctx, session.Origins)
	}

	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		if err := storage.ClearCookies().Do(actionCtx); err != nil {
			return fmt.Errorf("clear existing browser cookies: %w", err)
		}
		if err := network.SetCookies(params).Do(actionCtx); err != nil {
			return fmt.Errorf("set cookies: %w", err)
		}
		return nil
	})); err != nil {
		return err
	}

	return restoreOrigins(ctx, session.Origins)
}

func restoreOrigins(ctx context.Context, origins []PersistedOrigin) error {
	for _, origin := range origins {
		if origin.Origin == "" {
			continue
		}
		items := origin.LocalStorage
		if len(items) == 0 {
			continue
		}
		if err := chromedp.Run(ctx,
			chromedp.Navigate(origin.Origin),
			chromedp.Evaluate(`(() => localStorage.clear())()`, nil),
			chromedp.ActionFunc(func(actionCtx context.Context) error {
				for _, item := range items {
					js := fmt.Sprintf(`(() => localStorage.setItem(%q, %q))()`, item.Name, item.Value)
					if evalErr := chromedp.Evaluate(js, nil).Do(actionCtx); evalErr != nil {
						return evalErr
					}
				}
				return nil
			}),
		); err != nil {
			return fmt.Errorf("restore localStorage for %s: %w", origin.Origin, err)
		}
	}
	return nil
}

func EnsureLogin(ctx context.Context, cookiesPath string) error {
	if err := LoadCookies(ctx, cookiesPath); err != nil {
		return err
	}

	var currentURL string
	if err := chromedp.Run(ctx,
		network.Enable(),
		chromedp.Navigate(DefaultBaseURL),
		chromedp.Sleep(2*time.Second),
		chromedp.Location(&currentURL),
	); err != nil {
		return fmt.Errorf("navigate to protected page: %w", err)
	}

	if isAuthRedirect(currentURL) {
		return fmt.Errorf("saved cookies are not valid anymore")
	}

	return nil
}

func resolveWebSocketDebuggerURL(ctx context.Context, endpoint string) (string, error) {
	if endpoint == "" {
		return "", fmt.Errorf("empty CDP endpoint")
	}

	if strings.HasPrefix(endpoint, "ws://") || strings.HasPrefix(endpoint, "wss://") {
		return endpoint, nil
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}

	if u.Scheme == "" {
		u.Scheme = "http"
	}

	versionURL := *u
	versionURL.Path = strings.TrimRight(versionURL.Path, "/") + "/json/version"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build version request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("reach %s: %w", versionURL.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status from %s: %s", versionURL.String(), resp.Status)
	}

	var payload struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode websocket debugger URL: %w", err)
	}
	if payload.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("json/version response did not contain webSocketDebuggerUrl")
	}

	return payload.WebSocketDebuggerURL, nil
}

func isAuthRedirect(currentURL string) bool {
	return strings.Contains(currentURL, "/auth/") || strings.Contains(currentURL, "auth.inhrplus.com")
}
