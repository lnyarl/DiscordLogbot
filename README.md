# DiscordLogbot

Discord 서버의 채널별 대화를 PostgreSQL에 로깅하고, 웹 인터페이스에서 Discord 계정으로 로그인하여 검색할 수 있는 봇입니다.

## 구성

| 서비스 | 역할 |
|--------|------|
| `bot` | Discord 봇 — 메시지 실시간 수집 및 DB 저장 |
| `web` | FastAPI 웹 서버 — Discord OAuth2 로그인, Full-text 검색 UI (`:8080`) |
| `postgres` | PostgreSQL 16 — 로그 데이터 저장소 |

## 빠른 시작

### 1. 사전 준비

**Discord Bot 생성** ([discord.com/developers](https://discord.com/developers/applications))

1. New Application → Bot 탭 → Token 복사
2. Bot 탭 → Privileged Gateway Intents에서 **Message Content Intent** 및 **Server Members Intent** 활성화
3. OAuth2 탭 → URL Generator → `bot` + `applications.commands` 스코프, 권한 `Read Messages/View Channels` 선택 → 생성된 URL로 서버에 봇 초대

**Discord OAuth2 앱 설정** (같은 앱에서)

1. OAuth2 탭 → Redirects → `http://서버IP:8080/auth/callback` 추가
2. Client ID, Client Secret 복사

### 2. 환경변수 설정

```bash
cp .env.example .env
```

`.env` 파일 편집:

```env
# 봇 토큰 (Discord Developer Portal → Bot → Token)
DISCORD_TOKEN=your_bot_token_here

# DB
DB_BACKEND=postgresql
DATABASE_URL=postgresql://logbot:비밀번호@postgres:5432/logbot
POSTGRES_PASSWORD=비밀번호           # 원하는 비밀번호로 설정

# 웹 서비스 OAuth2
DISCORD_CLIENT_ID=your_client_id
DISCORD_CLIENT_SECRET=your_client_secret
DISCORD_REDIRECT_URI=http://서버IP:8080/auth/callback

# JWT 서명키 (랜덤 문자열)
JWT_SECRET=랜덤문자열
```

JWT_SECRET 생성:
```bash
openssl rand -hex 32
```

### 3. 실행

```bash
docker compose up -d
```

웹 서비스: `http://서버IP:8080`

---

## 봇 슬래시 커맨드

봇 초대 후 자동으로 슬래시 커맨드가 등록됩니다. 모든 커맨드는 **서버 관리 권한**이 필요합니다.

| 커맨드 | 설명 |
|--------|------|
| `/logbot add #채널` | 해당 채널 로깅 시작 |
| `/logbot remove #채널` | 해당 채널 로깅 중단 |
| `/logbot list` | 현재 로깅 중인 채널 목록 |
| `/logbot status` | 봇 상태 및 총 로그 수 |

> 로깅 채널을 하나도 지정하지 않으면 아무것도 로깅하지 않습니다. 반드시 `/logbot add`로 채널을 지정해야 합니다.

---

## 웹 검색 서비스

Discord 계정으로 로그인하면 **자신이 볼 수 있는 채널**의 메시지만 검색할 수 있습니다.

- 서버 필터, 채널 필터, 키워드 검색 지원
- PostgreSQL `pg_trgm` 기반 유사도 검색 (한국어/영어 모두 지원)
- 검색 결과에 키워드 하이라이트

---

## 운영

### 로그 확인

```bash
# 전체 로그
docker compose logs -f

# 봇만
docker compose logs -f bot

# 웹 서버만
docker compose logs -f web
```

### 재시작

```bash
docker compose restart bot
docker compose restart web
```

### 업데이트 배포

```bash
git pull
docker compose build
docker compose up -d
```

### 서비스 중단

```bash
docker compose down
```

> DB 데이터는 `postgres_data` Docker 볼륨에 유지됩니다. 데이터까지 삭제하려면 `docker compose down -v`.

### DB 직접 접속

```bash
docker compose exec postgres psql -U logbot -d logbot
```

유용한 쿼리:

```sql
-- 채널별 메시지 수
SELECT channel_name, COUNT(*) FROM messages GROUP BY channel_name ORDER BY COUNT(*) DESC;

-- 최근 100개 메시지
SELECT author_name, channel_name, content, created_at
FROM messages ORDER BY created_at DESC LIMIT 100;

-- 특정 키워드 검색
SELECT author_name, channel_name, content, created_at
FROM messages WHERE content % '검색어'
ORDER BY similarity(content, '검색어') DESC LIMIT 20;
```

### 백업

```bash
docker compose exec postgres pg_dump -U logbot logbot > backup_$(date +%Y%m%d).sql
```

복원:
```bash
cat backup_20240101.sql | docker compose exec -T postgres psql -U logbot -d logbot
```

---

## DB 스키마

자세한 스키마 설명은 [`SCHEMA.md`](./SCHEMA.md) 참조. Claude Desktop 등 AI 도구에서 DB 내용을 분석하거나 요약할 때 참고할 수 있습니다.

---

## 트러블슈팅

**봇이 메시지를 기록하지 않는다**
- Discord Developer Portal에서 **Message Content Intent**가 활성화되어 있는지 확인
- `/logbot status`로 봇 상태 확인
- `docker compose logs bot`으로 에러 확인

**웹 로그인 후 채널이 안 보인다**
- 봇이 해당 서버에 초대되어 있는지 확인
- Discord Developer Portal → OAuth2 → Redirects에 정확한 URI가 등록되어 있는지 확인
- 로그아웃 후 재로그인 (채널 권한은 로그인 시점에 계산됨)

**`docker compose up` 시 postgres 연결 실패**
- `.env`의 `POSTGRES_PASSWORD`와 `DATABASE_URL`의 비밀번호가 일치하는지 확인
- `docker compose ps`로 postgres 컨테이너 상태 확인

**슬래시 커맨드가 Discord에 안 보인다**
- 봇 초대 시 `applications.commands` 스코프가 포함되었는지 확인
- `docker compose logs bot`에서 `Synced N command(s)` 메시지 확인
- Discord 클라이언트를 새로고침 (Ctrl+R)
