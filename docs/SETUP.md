# 세팅 & 사용 가이드

DiscordLogbot의 처음 설치부터 일상 운영까지 전 과정을 다룹니다.

## 목차
1. [사전 준비 — Discord 앱 만들기](#1-사전-준비--discord-앱-만들기)
2. [환경변수 설정](#2-환경변수-설정)
3. [실행](#3-실행)
4. [봇에 채널 등록하기](#4-봇에-채널-등록하기)
5. [웹 검색 UI](#5-웹-검색-ui)
6. [MCP 클라이언트 연동](#6-mcp-클라이언트-연동)
7. [운영 — 로그·재시작·업데이트·백업](#7-운영--로그재시작업데이트백업)
8. [트러블슈팅](#8-트러블슈팅)

---

## 1. 사전 준비 — Discord 앱 만들기

[Discord Developer Portal](https://discord.com/developers/applications)에서 **New Application**을 만듭니다. 봇과 웹 OAuth 모두 같은 앱에서 처리할 수 있습니다.

### 1-1. Bot 토큰 발급

1. 좌측 메뉴 **Bot** → `Reset Token`으로 토큰 발급 → **즉시 복사** (한 번만 보입니다). 이 값이 `DISCORD_TOKEN`.
2. 같은 페이지의 **Privileged Gateway Intents**에서 다음 둘을 켭니다:
   - `Message Content Intent` — 메시지 본문 수신용
   - `Server Members Intent` — 멤버 입퇴장 이벤트용
3. (권장) `Public Bot` 옵션을 끄면 본인만 봇을 초대할 수 있습니다.

### 1-2. OAuth2 클라이언트 정보

1. 좌측 메뉴 **OAuth2** → `Client ID`, `Client Secret`을 복사. 각각 `DISCORD_CLIENT_ID`, `DISCORD_CLIENT_SECRET`.
2. **OAuth2 → Redirects**에 다음 URI들을 등록 (`{BASE_URL}`은 외부 접근 가능한 본인 도메인 또는 `http://서버IP:8080`):
   - `{BASE_URL}/auth/callback` — 웹 검색 UI 로그인용
   - `{BASE_URL}/oauth/discord_callback` — MCP OAuth 흐름용

### 1-3. 봇을 서버에 초대

1. **OAuth2 → URL Generator**에서:
   - Scopes: `bot`, `applications.commands`
   - Bot Permissions: 최소 `Read Messages/View Channels` (모더레이션·이벤트까지 보려면 `View Audit Log`도 권장)
2. 생성된 URL을 열어 봇을 자기 서버에 초대.

---

## 2. 환경변수 설정

```bash
cp .env.example .env
```

`.env`의 각 항목 설명:

| 변수 | 설명 |
|---|---|
| `DISCORD_TOKEN` | 1-1에서 복사한 봇 토큰 |
| `DB_BACKEND` | `postgresql` (권장) 또는 `sqlite` |
| `DATABASE_URL` | `postgresql://logbot:비밀번호@postgres:5432/logbot` 형태. 비밀번호는 `POSTGRES_PASSWORD`와 일치 |
| `POSTGRES_PASSWORD` | PostgreSQL 비밀번호. 자유롭게 설정 |
| `DISCORD_CLIENT_ID` / `DISCORD_CLIENT_SECRET` | 1-2에서 복사 |
| `DISCORD_REDIRECT_URI` | `{BASE_URL}/auth/callback` (Developer Portal에 등록한 값과 정확히 일치) |
| `JWT_SECRET` | 세션 JWT 서명키. **반드시** 랜덤 문자열로 채울 것 |
| `BASE_URL` | 외부에서 접근 가능한 서버 URL (예: `https://logbot.example.com`). MCP OAuth redirect 생성에 사용 |
| `MCP_CLIENT_IDS` | (선택) MCP 접속을 허용할 client_id 목록, 쉼표 구분. 미설정 시 모든 MCP 요청 거부 |
| `MCP_ALLOWED_REDIRECT_URIS` | (선택) localhost 외에 허용할 MCP 클라이언트 redirect URI |

### `JWT_SECRET` 생성

```bash
openssl rand -hex 32
```

> `JWT_SECRET`이 비어 있으면 웹 서버가 시작 시점에 종료됩니다.

---

## 3. 실행

```bash
docker compose up -d
```

서비스 구성:
- `bot` — Discord 게이트웨이 연결, 메시지·이벤트 수집
- `web` — FastAPI (포트 `8080`, 호스트 루프백 바인드). 검색 UI + MCP 서버 + OAuth 콜백
- `logbot-postgres` — PostgreSQL 16 (포트 `5431`, 호스트 루프백 바인드)

> `docker-compose.yml`은 외부 네트워크 `stashy-network`에 의존합니다. 본인 환경에 해당 네트워크가 없다면 `networks` 섹션을 본인 인프라(또는 단순 `bridge`)에 맞게 수정하세요. 외부에서 접근하려면 그 위에 리버스 프록시(Nginx, Caddy, Cloudflare Tunnel 등)로 `8080` 포트를 라우팅합니다.

상태 확인:
```bash
docker compose ps
docker compose logs -f bot      # "Logged in as ..." 출력 확인
docker compose logs -f web      # "Application startup complete." 출력 확인
```

---

## 4. 봇에 채널 등록하기

봇은 **명시적으로 등록된 채널만** 로깅합니다. 초대 직후엔 아무것도 기록하지 않습니다.

디스코드에서 (서버 관리 권한 필요):

```
/logbot add #일반          # 단일 채널 등록
/logbot add_all            # 봇이 보이는 모든 공개 텍스트 채널 등록
/logbot list               # 현재 로깅 중인 채널 목록
/logbot remove #채널       # 로깅 중단
/logbot status             # 봇 상태, 누적 메시지 수
/logbot search 키워드      # 디스코드 내에서 즉시 검색
```

이벤트(입퇴장·밴·역할·AutoMod 등)는 채널 등록과 무관하게 봇이 서버에 있는 한 자동 로깅됩니다.

---

## 5. 웹 검색 UI

`{BASE_URL}` 또는 `http://서버IP:8080` 접속 → **Discord로 로그인**.

- 로그인 시점에 사용자의 채널 권한이 계산되어, **본인이 실제로 볼 수 있는 채널의 메시지만** 결과에 노출됩니다.
- 서버 필터, 채널 필터, 키워드 입력 → `pg_trgm` 유사도 정렬, 키워드 하이라이트.
- 첨부파일과 커스텀 이모지는 봇이 다운로드해 정적 경로로 서빙됩니다 (`/attachments`, `/emojis`).

권한이 바뀌었는데 검색 결과에 반영되지 않으면 **로그아웃 후 재로그인**하세요. (게이트웨이 이벤트 기반 캐시 무효화도 함께 동작하지만, 즉시 반영을 보장하려면 재로그인이 가장 확실합니다.)

---

## 6. MCP 클라이언트 연동

DiscordLogbot은 MCP(Model Context Protocol) over HTTP+SSE를 내장합니다. Claude Desktop 같은 MCP 클라이언트가 디스코드 히스토리를 직접 쿼리할 수 있습니다.

### 6-1. 서버 측 준비

`.env`에 다음 값이 있어야 합니다:

```env
BASE_URL=https://your-domain.com
MCP_CLIENT_IDS=claude-desktop,my-custom-client      # 미설정 시 모든 요청 거부
# MCP_ALLOWED_REDIRECT_URIS=https://your-app.example.com/callback   # localhost 외 허용
```

엔드포인트:
- SSE: `{BASE_URL}/mcp/sse`
- POST messages: `{BASE_URL}/mcp/messages`
- OAuth: `{BASE_URL}/oauth/...`

### 6-2. 제공되는 툴

| 툴 | 용도 |
|---|---|
| `list_channels` | 사용자가 접근 가능한 서버/채널 목록 |
| `search_messages` | 채널에서 키워드 검색 (since/until 기간 필터 가능) |
| `get_messages` | 채널 메시지 조회 (기간 필터, 최대 500건) |
| `get_guild_events` | 서버 이벤트 조회 (event_type 필터, 기간 필터) |

모든 툴은 호출 시점마다 사용자 권한을 재확인합니다 — 봇이 게이트웨이 이벤트로 권한 캐시를 무효화하므로, 권한 변경이 즉시 반영됩니다.

### 6-3. Claude Desktop 예시

`claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "discord-logbot": {
      "url": "https://your-domain.com/mcp/sse"
    }
  }
}
```

최초 연결 시 OAuth 흐름이 트리거되어 디스코드 로그인을 요구합니다. 토큰은 클라이언트가 보관하고, 이후 자동 인증됩니다.

---

## 7. 운영 — 로그·재시작·업데이트·백업

### 로그
```bash
docker compose logs -f               # 전체
docker compose logs -f bot           # 봇만
docker compose logs -f web           # 웹/MCP만
```

### 재시작 / 업데이트
```bash
docker compose restart bot
docker compose restart web

# 코드 업데이트
git pull
docker compose build
docker compose up -d
```

### 서비스 중단
```bash
docker compose down                  # 컨테이너만 제거 (DB는 볼륨에 유지)
docker compose down -v               # ⚠️ DB까지 완전 삭제
```

### DB 직접 접속
```bash
docker compose exec logbot-postgres psql -U logbot -d logbot
```

자주 쓰는 쿼리:
```sql
-- 채널별 메시지 수
SELECT channel_name, COUNT(*) FROM messages
GROUP BY channel_name ORDER BY COUNT(*) DESC;

-- 최근 100개
SELECT author_name, channel_name, content, created_at
FROM messages ORDER BY created_at DESC LIMIT 100;

-- 유사도 검색
SELECT author_name, channel_name, content, created_at
FROM messages WHERE content % '검색어'
ORDER BY similarity(content, '검색어') DESC LIMIT 20;

-- 최근 길드 이벤트
SELECT event_type, actor_name, target_name, occurred_at
FROM guild_events ORDER BY occurred_at DESC LIMIT 50;
```

### 백업 / 복원
```bash
# 백업
docker compose exec logbot-postgres pg_dump -U logbot logbot \
  > backup_$(date +%Y%m%d).sql

# 복원
cat backup_20260101.sql | docker compose exec -T logbot-postgres psql -U logbot -d logbot
```

첨부파일·이모지는 호스트의 `./data/attachments`, `./data/emojis`에 저장되므로 별도 백업 대상에 포함하세요.

---

## 8. 트러블슈팅

**봇이 메시지를 기록하지 않는다**
- Developer Portal에서 `Message Content Intent`가 켜져 있는지
- `/logbot list`로 채널이 등록되어 있는지
- `docker compose logs bot`에서 게이트웨이 연결 / 에러 확인

**웹 로그인 후 채널이 안 보인다**
- 봇이 해당 서버에 있는지
- `DISCORD_REDIRECT_URI`가 Developer Portal의 Redirects 항목과 **글자 단위로** 일치하는지
- 권한이 최근에 바뀌었다면 로그아웃 후 재로그인

**MCP 클라이언트 연결이 거부된다 (`401 / 403`)**
- `.env`의 `MCP_CLIENT_IDS`에 클라이언트의 `client_id`가 등록되어 있는지 (미설정 시 전부 거부)
- Redirect URI가 `MCP_ALLOWED_REDIRECT_URIS` 또는 localhost에 해당하는지
- `BASE_URL`이 외부 접근 가능한 실제 URL인지

**`docker compose up` 시 postgres 연결 실패**
- `.env`의 `POSTGRES_PASSWORD`와 `DATABASE_URL` 비밀번호 일치
- `docker compose ps`로 `logbot-postgres` 컨테이너 healthy 상태 확인

**슬래시 커맨드가 디스코드에 안 보인다**
- 봇 초대 시 `applications.commands` 스코프 포함 여부
- `docker compose logs bot`에서 `Synced N command(s)` 로그 확인
- 디스코드 클라이언트 새로고침 (`Ctrl+R`)

**OAuth 콜백 시 `redirect_uri_mismatch`**
- `.env`의 `DISCORD_REDIRECT_URI`와 Developer Portal의 Redirects가 정확히 같은지 (트레일링 슬래시·http/https·포트 포함)
