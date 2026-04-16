# newrrowllm

`newrrowllm`은 BSSM NEWRROW CSR 플랫폼의 내장 에이전트와 터미널에서 대화하기 위한 Go CLI입니다.

이 도구는 다음 흐름을 목표로 합니다.

- 브라우저(CDP)에 로그인 세션을 1회 생성하고 저장
- 저장된 세션으로 이후 명령을 재사용
- `chat`, `status`, `logout`을 터미널에서 바로 실행

## 핵심 기능

- `newrrowllm auth`
  - CSR 플랫폼 로그인 세션을 생성하고 저장합니다.
  - `NEWRROW_EMAIL`, `NEWRROW_PASSWORD`가 있으면 로그인 폼을 자동 제출합니다.
  - 없으면 브라우저에서 직접 로그인한 뒤 완료를 진행합니다.
- `newrrowllm chat "메시지"`
  - 저장된 세션이 있으면 바로 재사용합니다.
  - 세션이 없거나 만료되면 인증을 다시 시도한 뒤 메시지를 보냅니다.
  - `NEWRROW_EMAIL`, `NEWRROW_PASSWORD`가 있으면 초기 채팅 명령 하나만으로도 세션을 만들고 메시지를 전송할 수 있습니다.
- `newrrowllm status`
  - 현재 저장된 세션이 유효한지 확인합니다.
- `newrrowllm logout`
  - 저장된 세션 파일을 삭제합니다.

## 요구 사항

- Go 1.21 이상
- CDP를 제공하는 브라우저 1개
  - 현재 기본값은 `http://127.0.0.1:9222`
  - Lightpanda 또는 Chrome 계열 브라우저의 원격 디버깅 포트를 여기에 맞추면 됩니다.
- BSSM NEWRROW CSR 접근 권한

## 설치

### 1) GitHub에서 바로 설치

```bash
go install github.com/bssm-oss/newrrow-llm@latest
```

설치 후 실행 파일은 일반적인 Go 바이너리 경로(`$GOBIN` 또는 `$GOPATH/bin`)에 생성됩니다.

### 2) 저장소를 받은 뒤 설치

```bash
git clone https://github.com/bssm-oss/newrrow-llm.git
cd newrrow-llm
make download
make build
```

또는:

```bash
go mod download
go install .
```

## CDP 브라우저 준비

이 CLI는 브라우저를 직접 띄우지 않습니다. 먼저 CDP 가능한 브라우저를 실행해야 합니다.

예시:

```bash
./lightpanda --host 127.0.0.1 --port 9222
```

`auth`를 쓸 때는 사용자가 로그인 과정을 볼 수 있도록 브라우저가 실제로 떠 있어야 합니다.

## 환경 변수

자주 쓰는 값은 아래처럼 환경 변수로 둘 수 있습니다.

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
- `NEWRROWM_AGENT_REPLY_SELECTOR` (호환용)

## 기본 사용법

### 1) 세션 생성

```bash
newrrowllm auth
```

동작 개요:

1. CDP 브라우저에 인증 페이지를 엽니다.
2. `NEWRROW_EMAIL`, `NEWRROW_PASSWORD`가 있으면 로그인 폼을 자동으로 제출합니다.
3. 필요하면 초대/홈 전환 단계를 통과합니다.
4. 세션 파일을 저장합니다.

### 2) 바로 채팅

```bash
newrrowllm chat "안녕, 오늘 일정 알려줘"
```

의도된 사용 경험은 이 명령 하나로 동작하는 것입니다.

- 저장된 세션이 있으면 그대로 사용합니다.
- 세션이 없거나 만료되면 내부적으로 인증을 시도한 뒤 메시지를 보냅니다.
- `NEWRROW_EMAIL`, `NEWRROW_PASSWORD`가 설정되어 있으면 아래처럼 바로 실행할 수 있습니다.

```bash
NEWRROW_EMAIL="your-id@example.com" \
NEWRROW_PASSWORD="your-password" \
newrrowllm chat "안녕, 오늘 일정 알려줘"
```

### 3) 세션 상태 확인

```bash
newrrowllm status
```

### 4) 로그아웃

```bash
newrrowllm logout
```

## 자주 쓰는 옵션

```bash
newrrowllm \
  --cdp-endpoint http://127.0.0.1:9222 \
  --cookies-path ~/.newrrowllm/cookies.json \
  --timeout 60s \
  --login-timeout 5m \
  chat "안녕"
```

## 선택자 커스터마이징

CSR 에이전트 UI는 고정되어 있지 않을 수 있으므로, 필요하면 선택자를 환경 변수나 설정으로 덮어쓸 수 있습니다.

기본적으로 아래 계열을 사용합니다.

- 입력창: `textarea`, `[contenteditable="true"]`, PromptBar 계열 클래스
- 런처: `NewrrowAgentFloatingButton` 계열 클래스
- 전송 버튼: `button[type="submit"]`, 전송 아이콘 버튼
- 응답 노드: `AgentMessage` 계열 클래스

## 설정 파일 예시

```yaml
cdp-endpoint: http://127.0.0.1:9222
base-url: https://bssm.newrrow.com/csr-platform/home
cookies-path: ~/.newrrowllm/cookies.json
timeout: 60s
login-timeout: 5m
chat-input-selector: 'textarea[class*="NewrrowAgentPromptBar-module__input"], textarea'
chat-send-selector: 'button[class*="NewrrowAgentPromptBar-module__sendMessageButton"], button[type="submit"]'
chat-launcher-selector: 'button[class*="NewrrowAgentFloatingButton-module__container"]'
chat-reply-selector: 'li[class*="AgentMessage-module__container"], [class*="AgentMessage-module__messageSection"]'
```

## 개발용 명령

```bash
make download
make build
make test
make fmt
```

## 현재 주의 사항

- 브라우저 CDP 세션 상태는 엔진별 차이가 있어서, Lightpanda와 일반 브라우저가 완전히 동일하게 동작하지 않을 수 있습니다.
- `auth`, `status`, `logout`은 기본적으로 CDP 브라우저가 있는 환경을 전제로 합니다.
- `chat`은 저장된 세션이 없을 때 환경 변수 기반 인증 bootstrap 경로를 사용할 수 있습니다.
- 실제 운영 전에 본인 환경에서 `chat`, `auth -> status -> chat -> logout` 흐름을 각각 확인하는 것이 좋습니다.
