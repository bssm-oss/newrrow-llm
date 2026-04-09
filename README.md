# newrrowllm

`newrrowllm` is a Go CLI for the BSSM NEWRROW CSR platform agent. It connects to a pre-started Lightpanda browser over CDP, persists the authenticated session, sends a chat message to the built-in CSR agent, and prints the reply in your terminal.

## Features

- `newrrowllm auth` opens the CSR platform in the connected browser, uses `NEWRROW_EMAIL` and `NEWRROW_PASSWORD` automatically when available, falls back to manual completion when they are not, continues through the invitation screen if it appears, and saves the authenticated session to `~/.newrrowllm/cookies.json`.
- `newrrowllm chat "hello"` restores the saved session, tries the browser UI path first, and falls back to the authenticated reply transport when Lightpanda does not render the launcher reliably.
- `newrrowllm status` checks whether the saved cookies still reach the protected CSR page without redirecting to auth.
- `newrrowllm logout` deletes the persisted cookie file.

## Prerequisites

- Go 1.21+
- A Lightpanda browser already running with CDP enabled on `127.0.0.1:9222`
- Access to the BSSM NEWRROW CSR platform

## Install Lightpanda

Follow the official installation instructions from Lightpanda:

- https://lightpanda.io/docs/

## Start Lightpanda CDP

This CLI does **not** launch Lightpanda for you. Start it yourself first.

Example:

```bash
./lightpanda --host 127.0.0.1 --port 9222
```

For `auth`, run Lightpanda in a visible mode so you can complete login manually in the browser window.

## Build

```bash
make download
make build
```

The binary will be created at `bin/newrrowllm`.

You can also install it with normal Go tooling:

```bash
go mod download
go install .
```

## Usage

### Authenticate and save cookies

```bash
bin/newrrowllm auth
```

Expected flow:

1. The CLI opens `https://bssm.newrrow.com/csr-platform/home` in the connected browser.
2. If `NEWRROW_EMAIL` and `NEWRROW_PASSWORD` are set, the CLI submits the login form automatically.
3. Otherwise, complete login manually in the visible browser and press Enter when prompted.
4. If the account lands on the invitation page first, the CLI continues to `/csr-platform/home` before saving the session.
5. The CLI saves the authenticated session to the configured cookie file.

### Send a chat message

```bash
bin/newrrowllm chat "안녕, 오늘 일정 알려줘"
```

If the saved session is missing or expired, the command fails with a prompt to run `newrrowllm auth` first.

### Check session status

```bash
bin/newrrowllm status
```

### Delete cookies

```bash
bin/newrrowllm logout
```

## Configuration

You can configure the CLI with flags, environment variables, or a `newrrowllm.yaml` file in the current directory or `~/.newrrowllm/`.

### Common flags

```bash
newrrowllm --cdp-endpoint http://127.0.0.1:9222 \
  --cookies-path ~/.newrrowllm/cookies.json \
  --timeout 60s \
  --login-timeout 5m
```

### Environment variables

- `NEWRROWLLM_CDP_ENDPOINT`
- `NEWRROWLLM_BASE_URL`
- `NEWRROWLLM_COOKIES_PATH`
- `NEWRROWLLM_TIMEOUT`
- `NEWRROWLLM_LOGIN_TIMEOUT`
- `NEWRROW_EMAIL`
- `NEWRROW_PASSWORD`
- `NEWRROWLLM_AGENT_INPUT_SELECTOR`
- `NEWRROWLLM_AGENT_SEND_SELECTOR`
- `NEWRROWLLM_AGENT_READY_SELECTOR`
- `NEWRROWLLM_AGENT_LAUNCHER_SELECTOR`
- `NEWRROWLLM_AGENT_REPLY_SELECTOR`
- `NEWRROWM_AGENT_REPLY_SELECTOR` (supported for compatibility with the requested name)

### Example config file

```yaml
cdp-endpoint: http://127.0.0.1:9222
base-url: https://bssm.newrrow.com/csr-platform/home
cookies-path: ~/.newrrowllm/cookies.json
timeout: 60s
login-timeout: 5m
chat-input-selector: 'textarea, [contenteditable="true"], .chat-input'
chat-send-selector: 'button[type="submit"], [aria-label="Send"], button:has(svg[data-icon="send"]), button:has(svg)'
chat-ready-selector: 'textarea[class*="NewrrowAgentPromptBar-module__input"], button[class*="NewrrowAgentFloatingButton-module__container"]'
chat-launcher-selector: 'button[class*="NewrrowAgentFloatingButton-module__container"]'
chat-reply-selector: 'li[class*="AgentMessage-module__container"], [class*="AgentMessage-module__messageSection"]'
```

## Customizing selectors

The CSR agent UI is not assumed to be stable. If the platform changes, override the selectors instead of changing code first.

Recommended targets:

- Input: `textarea`, `[contenteditable="true"]`, `.chat-input`
- Launcher: `button[class*="NewrrowAgentFloatingButton-module__container"]`
- Send button: `button[type="submit"]`, `[aria-label="Send"]`
- Reply nodes: assistant/agent/bot/response message containers

Because selectors are tried in order, place the most specific selector first.

## Error handling

- If Lightpanda is unavailable, the CLI prints: `Lightpanda CDP not running. Start it with: ./lightpanda --host 127.0.0.1 --port 9222`
- If cookies are missing, `chat` asks you to run `auth` first.
- If cookies are expired, `status` reports that the session expired and `chat` fails until you re-run `auth`.

## Development

```bash
make fmt
make test
make build
```

## Notes and limitations

- `auth` assumes you started Lightpanda externally in a visible mode.
- Because the exact CSR agent DOM is unknown, selector overrides may be required over time.
- The CLI persists the authenticated browser session in the configured JSON file and reuses it on later runs.
