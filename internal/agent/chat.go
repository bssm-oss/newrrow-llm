package agent

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"

	"github.com/yourusername/newrrowllm/internal/browser"
)

func SendChatMessage(ctx context.Context, cfg browser.Config, message string, logger *logrus.Logger) (string, error) {
	if _, err := os.Stat(cfg.CookiesPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("cookies not found. Run 'newrrowllm auth' first")
		}
		return "", fmt.Errorf("stat cookies: %w", err)
	}

	if err := browser.LoadCookies(ctx, cfg.CookiesPath); err != nil {
		return "", fmt.Errorf("load saved cookies: %w", err)
	}

	beforeCount, beforeText, err := navigateAndPrepare(ctx, cfg)
	if err != nil {
		return "", err
	}

	inputSelector, err := firstVisibleSelector(ctx, cfg.ActionTimeout, cfg.InputSelectors)
	if err != nil || inputSelector == "" {
		reply, fetchErr := sendMessageViaHTTPStream(cfg, message)
		if fetchErr == nil {
			return reply, nil
		}
		reply, browserFetchErr := sendMessageViaBrowserFetch(ctx, cfg, message)
		if browserFetchErr == nil {
			return reply, nil
		}
		return "", browserFetchErr
	}

	if err := setInputAndSend(ctx, inputSelector, message, logger, cfg.SendSelectors, cfg.ActionTimeout); err != nil {
		return "", err
	}

	reply, err := waitForReply(ctx, cfg, beforeCount, beforeText)
	if err != nil {
		return "", err
	}

	return reply, nil
}

func CheckStatus(ctx context.Context, cfg browser.Config) (bool, string, error) {
	if _, err := os.Stat(cfg.CookiesPath); err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("stat cookies: %w", err)
	}

	if err := browser.LoadCookies(ctx, cfg.CookiesPath); err != nil {
		return false, "", fmt.Errorf("load saved cookies: %w", err)
	}

	currentURL, authenticated, err := openAuthenticatedHome(ctx, cfg, cfg.ActionTimeout)
	if err != nil {
		return false, currentURL, err
	}
	if !authenticated {
		return false, currentURL, nil
	}
	return true, currentURL, nil
}

func navigateAndPrepare(ctx context.Context, cfg browser.Config) (int, string, error) {
	_, authenticated, err := openAuthenticatedHome(ctx, cfg, cfg.LoginTimeout)
	if err != nil {
		return 0, "", err
	}
	if !authenticated {
		return 0, "", fmt.Errorf("session expired. Run 'newrrowllm auth'")
	}

	if err := openAgentIfNeeded(ctx, cfg); err != nil {
		return 0, "", nil
	}

	count, err := replyCount(ctx, cfg.ActionTimeout, cfg.ReplySelectors)
	if err != nil {
		return 0, "", err
	}
	text, err := lastReplyText(ctx, cfg.ActionTimeout, cfg.ReplySelectors)
	if err != nil {
		return 0, "", err
	}
	return count, text, nil
}

func openAuthenticatedHome(ctx context.Context, cfg browser.Config, timeout time.Duration) (string, bool, error) {
	visitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	homeURL := authenticatedHomeURL(cfg.BaseURL)
	if err := chromedp.Run(visitCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		_, _, _, err := cdppage.Navigate(homeURL).Do(actionCtx)
		return err
	})); err != nil && !isTransientLikeAuthErr(err) {
		return "", false, fmt.Errorf("navigate to protected page: %w", err)
	}
	_ = completeInvitationIfPresent(visitCtx, homeURL)
	_ = chromedp.Run(visitCtx, chromedp.Sleep(3*time.Second))
	var currentURL string
	if err := chromedp.Run(visitCtx, chromedp.Location(&currentURL)); err != nil {
		if isTransientLikeAuthErr(err) {
			currentURL = homeURL
		} else {
			return currentURL, false, err
		}
	}
	if isAuthLikeURL(currentURL) {
		return currentURL, false, nil
	}
	return currentURL, strings.Contains(currentURL, "/csr-platform/"), nil
}

func completeInvitationIfPresent(ctx context.Context, baseURL string) error {
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return err
	}
	if !strings.Contains(currentURL, "/csr-platform/invitation") {
		return nil
	}
	if err := chromedp.Run(ctx,
		chromedp.Click(`a[href="/csr-platform/home"]`, chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
	); err == nil {
		return nil
	}
	return chromedp.Run(ctx,
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			_, _, _, err := cdppage.Navigate(baseURL).Do(actionCtx)
			return err
		}),
		chromedp.Sleep(1500*time.Millisecond),
	)
}

func authenticatedHomeURL(baseURL string) string {
	if strings.Contains(baseURL, "?") {
		return baseURL + "&login=true"
	}
	return baseURL + "?login=true"
}

func openAgentIfNeeded(ctx context.Context, cfg browser.Config) error {
	deadline := time.Now().Add(cfg.ActionTimeout)
	var launcherSelector string
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if inputSelector, err := firstVisibleSelector(ctx, 5*time.Second, cfg.InputSelectors); err == nil && inputSelector != "" {
			return nil
		}
		if selector, err := firstVisibleSelector(ctx, 5*time.Second, cfg.LauncherSelectors); err == nil && selector != "" {
			launcherSelector = selector
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if launcherSelector == "" {
		return fmt.Errorf("agent launcher not detected. Run 'newrrowllm auth' again or override the chat selectors")
	}

	if err := chromedp.Run(ctx, chromedp.Click(launcherSelector, chromedp.ByQuery)); err != nil {
		return fmt.Errorf("open agent UI with %q: %w", launcherSelector, err)
	}

	deadline = time.Now().Add(cfg.ActionTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if inputSelector, err := firstVisibleSelector(ctx, cfg.ActionTimeout, cfg.InputSelectors); err == nil && inputSelector != "" {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("chat input not detected after opening agent UI")
}

func sendMessageViaBrowserFetch(ctx context.Context, cfg browser.Config, message string) (string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, cfg.LoginTimeout)
	defer cancel()
	var result struct {
		Reply string `json:"reply"`
		Error string `json:"error"`
	}
	script := fmt.Sprintf(`(async () => {
		let answer = '';
		try {
			const readCookie = (name) => document.cookie.split('; ').find((item) => item.startsWith(name + '='))?.split('=').slice(1).join('=') || '';
			const token = readCookie('csrAccessToken');
			if (!token) return { error: 'missing csrAccessToken' };
			const payload = JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
			const tenant = payload.domain || 'bssm';
			const memberId = Number(payload.sub);
			if (!memberId) return { error: 'missing member id' };
			const commonHeaders = {
				'Authorization': 'Bearer ' + token,
				'Tenant': tenant,
				'Content-Type': 'application/json',
				'Accept': 'application/json, text/plain, */*'
			};
			const chatRes = await fetch('https://api-agw-backend.inhrplus.com/main/api/v1/nrow/agent/chats', {
				method: 'POST',
				credentials: 'include',
				headers: commonHeaders,
				body: JSON.stringify({ memberId })
			});
			if (!chatRes.ok) return { error: 'create chat failed: ' + chatRes.status };
			const chatData = await chatRes.json();
			const chatId = chatData?.contents?.id;
			if (!chatId) return { error: 'missing chat id' };
			const streamRes = await fetch('https://api-main.hrp.kr-pr-jainwon.com/api/v1/nrow/agent/chats/' + chatId + '/messages/stream', {
				method: 'POST',
				credentials: 'include',
				headers: { ...commonHeaders, 'Accept': '*/*' },
				body: JSON.stringify({ input: %q, period: 'WEEKLY' })
			});
			if (!streamRes.ok || !streamRes.body) return { error: 'stream failed: ' + streamRes.status };
			const reader = streamRes.body.getReader();
			const decoder = new TextDecoder('utf-8');
			let buffer = '';
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });
				const chunks = buffer.split('\n\n');
				buffer = chunks.pop() || '';
				for (const chunk of chunks) {
					const dataLine = chunk.split('\n').find((line) => line.startsWith('data:'));
					if (!dataLine) continue;
					const payload = JSON.parse(dataLine.slice(5));
					if (payload?.type === 'ANSWER_PART') answer += payload?.message?.content || '';
				}
			}
			return { reply: answer.trim() };
		} catch (error) {
			if (answer.trim()) {
				return { reply: answer.trim() };
			}
			return { error: String(error) };
		}
	})()`, message)
	if err := chromedp.Run(fetchCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
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
		if err := json.Unmarshal(res.Value, &result); err != nil {
			return err
		}
		return nil
	})); err != nil {
		return "", fmt.Errorf("send chat via browser fetch fallback: %w", err)
	}
	if strings.TrimSpace(result.Error) != "" {
		return "", fmt.Errorf("send chat via browser fetch fallback: %s", result.Error)
	}
	if strings.TrimSpace(result.Reply) == "" {
		return "", fmt.Errorf("agent returned an empty response")
	}
	return strings.TrimSpace(result.Reply), nil
}

func sendMessageViaHTTPStream(cfg browser.Config, message string) (string, error) {
	session, err := loadSessionForHTTP(cfg.CookiesPath)
	if err != nil {
		return "", err
	}

	client := session.httpClient(cfg.LoginTimeout)
	createReq, err := session.newJSONRequest(http.MethodPost, "https://api-agw-backend.inhrplus.com/main/api/v1/nrow/agent/chats", map[string]int{"memberId": session.MemberID})
	if err != nil {
		return "", err
	}
	createResp, err := client.Do(createReq)
	if err != nil {
		return "", fmt.Errorf("send chat via http fallback (create chat): %w", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode < 200 || createResp.StatusCode >= 300 {
		return "", fmt.Errorf("send chat via http fallback (create chat): status %s", createResp.Status)
	}
	var createData struct {
		Contents struct {
			ID int `json:"id"`
		} `json:"contents"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&createData); err != nil {
		return "", fmt.Errorf("send chat via http fallback (decode create chat): %w", err)
	}
	if createData.Contents.ID == 0 {
		return "", fmt.Errorf("send chat via http fallback: missing chat id")
	}

	streamReq, err := session.newJSONRequest(http.MethodPost, fmt.Sprintf("https://api-main.hrp.kr-pr-jainwon.com/api/v1/nrow/agent/chats/%d/messages/stream", createData.Contents.ID), map[string]string{"input": message, "period": "WEEKLY"})
	if err != nil {
		return "", err
	}
	streamReq.Header.Set("Accept", "*/*")
	streamResp, err := client.Do(streamReq)
	if err != nil {
		return "", fmt.Errorf("send chat via http fallback (stream): %w", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode < 200 || streamResp.StatusCode >= 300 {
		return "", fmt.Errorf("send chat via http fallback (stream): status %s", streamResp.Status)
	}

	reader := bufio.NewReader(streamResp.Body)
	var answer strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(strings.TrimSpace(line), "data:") {
			var payload struct {
				Type    string `json:"type"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}
			if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:"))), &payload); jsonErr == nil {
				if payload.Type == "ANSWER_PART" {
					answer.WriteString(payload.Message.Content)
				}
			}
		}
		if err != nil {
			break
		}
	}
	if strings.TrimSpace(answer.String()) == "" {
		return "", fmt.Errorf("send chat via http fallback: empty reply")
	}
	return strings.TrimSpace(answer.String()), nil
}

type httpSession struct {
	Cookies  []browser.PersistedCookie
	Token    string
	Tenant   string
	MemberID int
}

func loadSessionForHTTP(path string) (*httpSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read saved session: %w", err)
	}
	var session browser.PersistedSession
	if err := json.Unmarshal(data, &session); err != nil || session.Cookies == nil {
		var legacy []browser.PersistedCookie
		if legacyErr := json.Unmarshal(data, &legacy); legacyErr != nil {
			if err != nil {
				return nil, fmt.Errorf("parse saved session: %w", err)
			}
			return nil, fmt.Errorf("parse saved session: %w", legacyErr)
		}
		session = browser.PersistedSession{Cookies: legacy}
	}
	var token string
	for _, cookie := range session.Cookies {
		if cookie.Name == "csrAccessToken" {
			token = cookie.Value
			break
		}
	}
	if token == "" {
		return nil, fmt.Errorf("saved session does not contain csrAccessToken")
	}
	payload, err := decodeCSRToken(token)
	if err != nil {
		return nil, err
	}
	return &httpSession{Cookies: session.Cookies, Token: token, Tenant: payload.Domain, MemberID: payload.Sub}, nil
}

func decodeCSRToken(token string) (struct {
	Sub    int
	Domain string
}, error) {
	var payload struct {
		Sub    string `json:"sub"`
		Domain string `json:"domain"`
	}
	var result struct {
		Sub    int
		Domain string
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return result, fmt.Errorf("invalid csrAccessToken")
	}
	raw := parts[1]
	if rem := len(raw) % 4; rem != 0 {
		raw += strings.Repeat("=", 4-rem)
	}
	decoded, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return result, fmt.Errorf("decode csrAccessToken payload: %w", err)
	}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return result, fmt.Errorf("parse csrAccessToken payload: %w", err)
	}
	if payload.Sub == "" {
		return result, fmt.Errorf("csrAccessToken missing subject")
	}
	var sub int
	if _, err := fmt.Sscanf(payload.Sub, "%d", &sub); err != nil {
		return result, fmt.Errorf("parse csrAccessToken subject: %w", err)
	}
	result.Sub = sub
	if payload.Domain == "" {
		result.Domain = "bssm"
	} else {
		result.Domain = payload.Domain
	}
	return result, nil
}

func (s *httpSession) httpClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func (s *httpSession) newJSONRequest(method, rawURL string, body any) (*http.Request, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, rawURL, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Tenant", s.Tenant)
	req.Header.Set("Origin", "https://bssm.newrrow.com")
	req.Header.Set("Referer", "https://bssm.newrrow.com/csr-platform/home?login=true")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "ko-KR,ko;q=0.9")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Storage-Access", "active")
	req.Header.Set("Priority", "u=1, i")
	req.Header.Set("Cookie", s.cookieHeader())
	return req, nil
}

func (s *httpSession) cookieHeader() string {
	parts := make([]string, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func setInputAndSend(ctx context.Context, inputSelector, message string, logger *logrus.Logger, sendSelectors []string, timeout time.Duration) error {
	if err := chromedp.Run(ctx,
		chromedp.Click(inputSelector, chromedp.ByQuery),
		chromedp.SendKeys(inputSelector, message, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("type message into %q: %w", inputSelector, err)
	}

	sendSelector, err := firstVisibleSelector(ctx, timeout, sendSelectors)
	if err == nil && sendSelector != "" {
		logger.Debugf("Using send button selector: %s", sendSelector)
		if clickErr := chromedp.Run(ctx, chromedp.Click(sendSelector, chromedp.ByQuery)); clickErr == nil {
			return nil
		}
	}

	if err := chromedp.Run(ctx, chromedp.KeyEvent("\n")); err != nil {
		return fmt.Errorf("submit chat message: %w", err)
	}
	return nil
}

func waitForReply(ctx context.Context, cfg browser.Config, beforeCount int, beforeText string) (string, error) {
	deadline := time.Now().Add(cfg.LoginTimeout)
	baseline := strings.TrimSpace(beforeText)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count, err := replyCount(ctx, cfg.ActionTimeout, cfg.ReplySelectors)
		if err != nil {
			return "", err
		}
		text, err := lastReplyText(ctx, cfg.ActionTimeout, cfg.ReplySelectors)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) != "" && (count > beforeCount || strings.TrimSpace(text) != baseline) {
			return text, nil
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return "", fmt.Errorf("timed out waiting for the agent reply")
}

func firstVisibleSelector(ctx context.Context, timeout time.Duration, selectors []string) (string, error) {
	script, err := browser.FirstVisibleSelectorScript(selectors)
	if err != nil {
		return "", err
	}
	var selector string
	evalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := chromedp.Run(evalCtx, chromedp.Evaluate(script, &selector)); err != nil {
		return "", err
	}
	return selector, nil
}

func replyCount(ctx context.Context, timeout time.Duration, selectors []string) (int, error) {
	script, err := browser.CountVisibleMatchesScript(selectors)
	if err != nil {
		return 0, err
	}
	var count int
	evalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := chromedp.Run(evalCtx, chromedp.Evaluate(script, &count)); err != nil {
		return 0, fmt.Errorf("count reply nodes: %w", err)
	}
	return count, nil
}

func lastReplyText(ctx context.Context, timeout time.Duration, selectors []string) (string, error) {
	script, err := browser.LastVisibleTextScript(selectors)
	if err != nil {
		return "", err
	}
	var text string
	evalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := chromedp.Run(evalCtx, chromedp.Evaluate(script, &text)); err != nil {
		return "", fmt.Errorf("extract last reply text: %w", err)
	}
	return strings.TrimSpace(text), nil
}

func isAuthLikeURL(currentURL string) bool {
	return strings.Contains(currentURL, "/auth/") || strings.Contains(currentURL, "auth.inhrplus.com") || currentURL == ""
}

func isTransientLikeAuthErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "context canceled") || strings.Contains(msg, "invalid context") || strings.Contains(msg, "Cannot find default execution context")
}
