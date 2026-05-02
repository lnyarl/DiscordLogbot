# DiscordLogbot

> Discord 서버의 메시지·이벤트·모더레이션을 영구 보관하고, 권한대로 검색하고, AI 어시스턴트에 그대로 연결하세요. 셀프호스팅, 데이터는 당신 서버에만.

Discord 기본 검색은 짧고, 휘발되고, AI에는 닿지 않습니다. DiscordLogbot은 봇 + 웹 검색 UI + MCP 서버를 한 번에 띄워 — 서버 히스토리를 **당신의 PostgreSQL**에 쌓고, Discord 권한 그대로 검색·쿼리하게 해줍니다.

## Features

- **풀텍스트 검색** — `pg_trgm` 기반 한/영 유사도 검색, 첨부·커스텀 이모지 로컬 보존
- **메시지 + 이벤트 + 모더레이션** — 멤버 입퇴장·밴·역할 변경·채널 변경·AutoMod 액션·예약 이벤트까지 로깅
- **권한 기반 웹 UI** — Discord OAuth 로그인 → 본인이 실제로 볼 수 있는 채널만 검색 가능
- **MCP 서버 내장** — Claude Desktop 등 MCP 클라이언트가 SSE로 연결해 히스토리를 쿼리 (사용자 권한 그대로)
- **데이터 주권** — 수집한 데이터는 어떤 외부 서비스로도 전송되지 않음. 전부 로컬 PostgreSQL에

## Quick Start

> 자세한 단계별 가이드와 운영·트러블슈팅은 [**docs/SETUP.md**](docs/SETUP.md)를 보세요.

### 1. Discord 앱 준비
[Developer Portal](https://discord.com/developers/applications) → New Application:
- **Bot** 탭: Token 발급 + `Message Content` & `Server Members` Intent 활성화
- **OAuth2 → Redirects**: `{BASE_URL}/auth/callback`, `{BASE_URL}/oauth/discord_callback` 등록
- 봇 초대: `bot` + `applications.commands` 스코프, 권한 `Read Messages/View Channels`

### 2. 환경변수
```bash
cp .env.example .env
# DISCORD_TOKEN, DISCORD_CLIENT_ID/SECRET, DISCORD_REDIRECT_URI,
# POSTGRES_PASSWORD, JWT_SECRET, BASE_URL 채우기
openssl rand -hex 32   # JWT_SECRET 생성용
```

### 3. 실행
```bash
docker compose up -d
```
웹: `http://서버IP:8080` · MCP: `{BASE_URL}/mcp/sse`

### 4. 채널 등록
디스코드에서 슬래시 커맨드:
```
/logbot add #채널        # 또는 /logbot add_all
/logbot status
```

> 채널을 하나도 등록하지 않으면 아무것도 로깅하지 않습니다.

## 슬래시 커맨드

`/logbot` — **서버 관리** 권한 보유자 전용.

| 커맨드 | 동작 |
|---|---|
| `add #채널` / `add_all` | 로깅 시작 (개별 / 전체 공개 텍스트 채널) |
| `remove #채널` | 로깅 중단 |
| `list` | 현재 로깅 중인 채널 목록 |
| `search <키워드>` | 서버 내 즉시 검색 |
| `status` | 봇 상태 + 누적 로그 수 |

## Docs

- [세팅 & 사용 가이드](docs/SETUP.md) — Discord 앱 발급부터 OAuth, MCP 연동, 백업, 트러블슈팅까지

## Support

이 프로젝트가 도움이 되셨다면 커피 한 잔 사주세요. 작은 응원이 큰 동기가 됩니다.

<p align="center">
  <a href="https://buymeacoffee.com/lnyarl" target="_blank">
    <img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me A Coffee" height="60">
  </a>
</p>
