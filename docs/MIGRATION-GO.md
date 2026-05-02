# Go 마이그레이션 기획서

> **상태:** 초안 (검토 대기) · **브랜치:** `migration/go-rewrite` · **작성일:** 2026-05-02

DiscordLogbot의 Python 구현을 Go로 전면 재작성하기 위한 계획. 기능 추가는 없고, **기존 동작을 1:1로 보존하면서 런타임 안정성·리소스 효율·배포 단순성을 얻는 것**이 목표다.

---

## 1. 배경과 동기

### 1.1 현재 시스템

| 컴포넌트 | 스택 | 역할 |
|---|---|---|
| `bot` | discord.py 2.3+ | 디스코드 게이트웨이 연결, 메시지·이벤트·모더레이션 수집 |
| `web` | FastAPI + Jinja2 + asyncpg | OAuth 로그인, 권한 기반 검색 UI, MCP 서버, 자체 OAuth authorization server |
| `worker` | Ollama HTTP 클라이언트 + asyncpg + pgvector | 메시지 임베딩 생성 (contextual retrieval) |
| `postgres` | PostgreSQL 16 + `pg_trgm` + `pgvector` | 영속 저장소 |

### 1.2 운영하면서 드러난 Python의 약점

1. **런타임 타입 에러가 늦게 발견된다.** `db.save_guild_event(...)` 시그니처를 바꿨을 때, 드물게 발동되는 핸들러(AutoMod 룰 변경 등)는 실제 이벤트 발생까지 며칠이 걸려야 발견된다. mypy strict로도 100% 보장 불가.
2. **메모리 풋프린트.** 봇 + 웹 합쳐 idle 200~300MB. VPS급에선 무관하지만 작은 박스에서는 부담.
3. **단일 바이너리 배포 불가.** Docker compose 외 옵션이 없어 셀프호스팅 OSS의 진입장벽이 높다.
4. **asyncio 함정.** 동기 호출 한 줄로 게이트웨이가 멈추는 부류의 누적 버그.

### 1.3 Go가 해결하는 부분

| 약점 | Go에서 |
|---|---|
| 런타임 타입 에러 | 컴파일 시점에 시그니처/필드 불일치 차단. 배포 자체가 막힘 |
| 메모리 풋프린트 | 봇+웹 합쳐 30~60MB 예상 (5~10× 절감) |
| 단일 바이너리 | `go build`로 정적 바이너리 → Docker 없이 셀프호스팅 가능 |
| asyncio 블로킹 | goroutine은 OS 스레드에 분산되어 한 핸들러 블로킹이 게이트웨이를 안 끊음 |

---

## 2. 목표 / 비목표

### 2.1 목표

- 봇 / 웹 / MCP / 임베딩 워커를 **Go로 전부 재작성**
- 기존 PostgreSQL **스키마와 데이터 그대로 사용** (마이그레이션 없음)
- 외부 인터페이스 호환 유지: 슬래시 커맨드, 웹 UI URL, MCP 엔드포인트, Ollama API 사용 패턴
- Docker compose 운영 모델 유지 (단일 바이너리 배포 옵션을 추가로 제공)

### 2.2 비목표 (이번 작업에서 하지 않는 것)

- 기능 추가, 리팩터링, UI 리디자인
- 새로운 DB 스키마 / 새 인덱스 (성능 이슈 발견 시 별도 작업)
- 새 라이브러리·모델·API 도입 (Voyage 같은 외부 임베딩 등)
- 프로세스 통합 — 봇/웹/워커는 각각 독립 바이너리(`cmd/bot`, `cmd/web`, `cmd/worker`)로만 빌드. 단일 통합 바이너리(`cmd/all`)는 만들지 않음 (운영상 분리 유지)
- **SQLite 백엔드 지원** — Go 구현은 PostgreSQL 전용. Python 잔재(`db/sqlite_db.py`, `db/factory.py`의 분기, `.env.example`의 SQLite 항목)는 Phase 9 컷오버 시 Python 디렉토리와 함께 자연 제거

---

## 3. 기술 스택 결정

| 영역 | 라이브러리 | 근거 |
|---|---|---|
| Discord | `github.com/bwmarrin/discordgo` | 사용 중인 모든 이벤트(50+)·intent·REST 1:1 매핑 확인됨 |
| MCP | `github.com/modelcontextprotocol/go-sdk` | 공식 SDK, Google 공동, `AddTool[In, Out]` 제네릭으로 자동 JSON Schema |
| HTTP | `github.com/go-chi/chi/v5` (Go 1.22+ `net/http` 위) | 라우트 그룹별 미들웨어 + 빌트인 `Recoverer`/`Logger`/`Timeout`/`RealIP`/`RequestID`. 직접 wrapper 5개 작성 비용 회피. zero transitive deps |
| Postgres | `github.com/jackc/pgx/v5` + `pgxpool` | jsonb·array 네이티브, asyncpg 등가 |
| pgvector | `github.com/pgvector/pgvector-go` | pgx 통합 |
| JWT | `github.com/golang-jwt/jwt/v5` | python-jose HS256 등가 |
| OAuth 서버 | **자체 구현** | 현재 Python도 ~350줄 자체 구현. golang-jwt로 직역 |
| Rate limit | `golang.org/x/time/rate` | slowapi 등가 |
| Ollama | `github.com/ollama/ollama/api` | 공식 Go 클라이언트 (Ollama 본체 동봉) |
| 동시성 | `golang.org/x/sync/errgroup`, `x/sync/semaphore` | asyncio.gather / Semaphore 등가 |
| 환경변수 | `github.com/joho/godotenv` | python-dotenv 등가 |
| 템플릿 | 표준 `html/template` | Jinja2 등가 |
| Discord OAuth 클라이언트 | 표준 `net/http` | 별도 SDK 불필요 |

**핵심 의존성 7개** (discordgo, mcp-go-sdk, chi, pgx, pgvector-go, golang-jwt, ollama-api). 그 외 작은 유틸(`godotenv`, `x/sync`, `x/time/rate`)과 표준 라이브러리로 채움.

---

## 4. 아키텍처

### 4.1 런타임 컴포넌트

```
┌──────────────────────────────────────────────────────────────┐
│                    Discord Gateway (WSS)                      │
└──────────────┬───────────────────────────────────────────────┘
               │
               ▼
        ┌────────────┐         ┌──────────────────────┐
        │   bot      │────────►│  postgres + pgvector │◄──┐
        │ (Go)       │         │                      │   │
        └────────────┘         └──────────┬───────────┘   │
                                          │               │
        ┌────────────┐                    │       ┌───────┴──────┐
        │   web      │────────────────────┘       │   worker     │
        │ (Go)       │                            │   (Go)       │
        └─────┬──────┘                            └──────┬───────┘
              │                                          │
        ┌─────┴──────┐                                   ▼
        │ MCP / OAuth│                            ┌────────────┐
        │ Discord OAuth│                          │   Ollama   │
        └────────────┘                            └────────────┘
```

### 4.2 Go 모듈 구조 (제안)

```
.
├── cmd/
│   ├── bot/main.go              # Discord 게이트웨이 진입점
│   ├── web/main.go              # FastAPI 등가
│   └── worker/main.go           # 임베딩 워커
├── internal/
│   ├── discord/                 # discordgo 래퍼, intents, 이벤트 핸들러
│   │   ├── logging.go
│   │   ├── guild_events.go
│   │   ├── moderation.go
│   │   ├── scheduled_events.go
│   │   ├── integration.go
│   │   ├── cache_invalidation.go
│   │   └── slash_commands.go
│   ├── db/                      # pgxpool, pgvector 등록, 마이그레이션 실행
│   │   ├── pool.go
│   │   ├── messages.go
│   │   ├── guild_events.go
│   │   └── permissions_cache.go
│   ├── permissions/             # 채널 접근 권한 계산 (핵심 위험 영역)
│   ├── mcp/                     # MCP 서버, 툴 핸들러
│   ├── auth/                    # JWT, Discord OAuth 클라이언트
│   ├── oauth/                   # MCP OAuth authorization server (자체 구현)
│   ├── search/                  # pg_trgm + pgvector 검색 핸들러
│   ├── attachments/             # CDN 다운로드, 커스텀 이모지 파싱
│   └── embedding/               # 컨텍스트 조합, Ollama 클라이언트, 토큰 추정
├── web/static/                  # 기존 그대로 재사용
├── web/templates/               # Jinja2 → html/template 포맷 변환
├── db/migrations/               # 기존 SQL 그대로 재사용
├── docker-compose.yml           # 변경 (Python 이미지 → Go 이미지)
└── Dockerfile.{bot,web,worker}  # 멀티 스테이지 빌드
```

### 4.3 외부 인터페이스 호환

| 인터페이스 | 변화 |
|---|---|
| 슬래시 커맨드 (`/logbot ...`) | 동일 (시그니처 보존) |
| 웹 검색 URL (`/`, `/auth/...`, `/search`) | 동일 |
| MCP 엔드포인트 (`/mcp/sse`, `/mcp/messages`) | 동일 |
| MCP OAuth 메타데이터 (`/.well-known/oauth-authorization-server`) | 동일 |
| Ollama API 호출 패턴 | 동일 |
| 환경변수 (`.env.example`) | 동일 (단, Go 추가 변수 있을 수 있음) |
| DB 스키마 | 동일 |

---

## 5. 단계별 마이그레이션 계획

각 Phase는 **이전 Phase 완료 기준(DoD)을 통과한 상태에서만** 시작.

### Phase 0 — 프로젝트 부트스트랩
- `go.mod` 초기화 (`module github.com/lnyarl/discordlogbot`)
- 디렉토리 스켈레톤 생성 (위 4.2 구조)
- `Dockerfile.bot/web/worker` 멀티 스테이지 빌드
- `docker-compose.yml`에 Go 서비스 추가 (Python 서비스와 병기, 컷오버 시점에 교체)
- 공통 환경변수 로딩, 로거(`log/slog`), pgxpool 초기화 코드
- 모든 Go 서비스에 `GET /health` 엔드포인트 (200 OK)
- `docker-compose.yml` healthcheck를 Python 의존 명령(`python -c ...`)에서 `wget`/`curl` 또는 Dockerfile `HEALTHCHECK CMD`로 교체 — Go 컨테이너에서도 동작 보장
- **마이그레이션 멱등성 보강** — 현재 모든 마이그레이션 SQL은 `IF NOT EXISTS` 가드로 멱등이지만, `db/migrate.py`의 `INSERT INTO _migrations (name) VALUES ($1)`이 PRIMARY KEY 충돌로 두 번째 인스턴스 startup 시 트랜잭션 롤백을 일으킨다. **`ON CONFLICT (name) DO NOTHING` 한 줄 추가**로 멱등화. Python migrate.py와 Go 직역 양쪽에 동일 패턴 적용 → 병행 기간 양쪽 startup 안전.
- **DoD:** `go build ./...` 성공, 모든 서비스 컨테이너 기동 + healthcheck 통과 (`docker compose ps`에 `healthy` 표시)

### Phase 1 — 권한 계산 동등성 (POC, 가장 큰 위험)
- `internal/permissions`에 `permissions.py` 직역
  - @everyone → 역할 deny 합산 → 역할 allow 합산 → 멤버 overwrite 순서 엄수
  - ADMINISTRATOR / 카테고리 overwrites / 채널 overwrites
- Python 워커 + Go 워커 양쪽이 같은 (user_id, guild_id) 표본 100건에 대해 채널 권한 계산 → 결과 diff
- **DoD:** 모든 표본에서 결과 일치. 차이 발생 시 직역 버그 수정.

### Phase 2 — MCP 보안 동등성 (POC)
- `internal/mcp`에 `list_channels` 툴 1개만 구현
- JWT 인증 미들웨어 (Bearer + `type=mcp_access` 검증)
- SSE transport: 공식 SDK의 `NewSSEHandler(getServer, opts)` 사용
- **검증:** "타인 session_id로 메시지 주입 차단" 시나리오를 `httptest` 자동화 케이스로 박음 (다른 user의 JWT로 같은 session_id에 POST → 403 응답 검증). SDK 버전업 시 회귀 자동 감지.
- **DoD:** 동일 JWT로 Python/Go에서 `list_channels` 결과 동일. `httptest` 보안 시나리오 통과. `go test -race ./internal/mcp/...` 통과 (session→user 매핑 동시 접근 race 검증).

### Phase 3 — 봇 메시지 로깅
- discordgo 클라이언트 + intents 매핑
- `MessageCreate`/`Update`/`Delete`/`DeleteBulk` 핸들러
- 첨부파일 + 커스텀 이모지 다운로드 (`<a?:name:id>` 정규식 직역)
- 핀 캐시 + `ChannelPinsUpdate`
- 슬래시 커맨드: `/logbot add/add_all/remove/list/search/status`
- **DoD:** 같은 길드/채널에 봇을 동시 띄울 수 없으니, **별도 테스트 길드 + 테스트 봇 토큰**에서 Python과 동일한 메시지 적재 확인.

### Phase 4 — 길드 이벤트 / 모더레이션 / 통합
- `guild_events_cog`, `moderation_cog`, `scheduled_events_cog`, `integration_cog` 직역
- `ThreadMembersUpdate` 배치를 `thread_member_join`/`remove` 두 이벤트로 분기
- `MessageDeleteBulk`는 ID 리스트만 (Python의 캐시 분기와 동등)
- `cache_invalidation_cog` 직역 (`channel_access_cache` 무효화)
- **DoD:** 테스트 길드에서 입퇴장·역할 변경·AutoMod 룰 생성·예약 이벤트 트리거 후 `guild_events` 테이블 row 일치.

### Phase 5 — 웹 검색 UI
- `web/main.py` + `web/auth.py` + `web/search.py` + `web/cache_admin.py` 직역
- **권한 모델 통일**: 웹 검색이 현재 JWT의 `guild_ids` 클레임을 쓰는데, MCP와 동일하게 `channel_access_cache` 테이블로 통일. 캐시는 봇의 무효화 이벤트로 항상 fresh. **Phase 5 진입 전 Python `web/search.py`도 같은 모델로 먼저 변경**해서 양쪽 동작을 일치시킨 뒤 Go 직역.
- **템플릿 변환 패턴 (Jinja2 → `html/template`)**:
  - 구문: `{% if %}/{% for %}` → `{{ if }}/{{ range }}`, `{{ var }}` 변수 표기는 동일
  - **Jinja2 globals (`bot_invite_url` 등)** → Go 측에서 모든 핸들러가 사용하는 공통 `templateData` 구조체 또는 base context map. 핸들러는 이 구조체를 채워 `tmpl.Execute(w, data)`에 넘김
  - 매크로 / 필터 → `template.FuncMap`에 함수 등록
  - 자동 escape는 `html/template`이 기본 활성 (Jinja2와 동등)
- **동적 파라미터 번호링 패턴 (`pgx`)**: `search.py`의 author 필터 유무에 따라 `$5` 자리가 바뀌는 f-string 패턴은 Go에서 **conditions/params 슬라이스 빌더**로 표현 — `mcp_router.py`의 `_append_time_filter`와 동일 패턴. 예시:
  ```go
  conditions := []string{"guild_id = $1"}
  params := []any{guildID}
  if author != "" {
      params = append(params, author)
      conditions = append(conditions, fmt.Sprintf("author_name = $%d", len(params)))
  }
  query := fmt.Sprintf("SELECT ... WHERE %s", strings.Join(conditions, " AND "))
  ```
- 정적 파일 서빙 (`/static`, `/attachments`, `/emojis`)
- Discord OAuth (web 로그인용) 자체 구현
- Rate limit 미들웨어 (`x/time/rate`)
- **DoD:** 동일 URL·쿠키 흐름. 검색 결과가 Python과 일치 (같은 query·같은 사용자). 권한 모델 통일 후 같은 사용자가 검색 UI와 MCP `list_channels`에서 **동일한 채널 집합**을 봄.

### Phase 6 — MCP 풀 + OAuth Authorization Server
- 나머지 MCP 툴: `search_messages`, `get_messages`, `get_guild_events`
- since/until 시간 필터 (ISO 8601 → DB text 비교 호환 형식 변환)
- `oauth_server.py` 직역: `/oauth/authorize`, `/oauth/token`, `/.well-known/...`, `/oauth/discord_callback`
- PKCE S256 + JWT auth code (`type=mcp_auth_code`, `jti` 일회성, `cc` challenge)
- Static `MCP_CLIENT_IDS` 화이트리스트 + localhost 자동 허용 redirect URI
- **DoD:** Claude Desktop이 Go MCP 서버에 OAuth로 연결, 4개 툴 모두 동작. Python과 응답 동일. OAuth 엔드포인트(`authorize`, `token`, `discord_callback`, `.well-known/oauth-authorization-server`)의 `httptest` 통합 테스트 통과: PKCE happy path, invalid `client_id` 거부, JTI 재사용 거부, 만료된 auth code 거부.

### Phase 7 — 임베딩 워커
- `cmd/worker/main.go`에 `workers/embedding_worker.py` 직역
- `pgxpool.Config.AfterConnect`에 `pgvector.RegisterTypes(ctx, conn)` 등록 ⚠️
- Ollama 호출 시 `http.Client.Timeout` + 명시적 backoff retry **추가** (현재 Python은 재시도 없음 — 마이그레이션 기회에 안정성 향상)
- `[]float64` → `[]float32` 변환 (Ollama 응답 → pgvector 입력). **정밀도 손실 없음** — DB는 어차피 float32로 저장하므로 Python도 동일한 자릿수로 떨어진다. 비교는 항상 "DB에 저장된 값" 기준으로 수행
- 한글/ASCII 토큰 추정 함수 직역 (`unicode` 패키지)
- 컨텍스트 조합 + gap trim 로직 직역
- **DoD:** 같은 메시지 셋을 Python 워커로 처리(임베딩 적재) → 동일 메시지를 Go 워커로 재처리(임베딩 갱신) → **DB에서 읽어온 vector** 기준으로 cosine ≥ 0.9999. 양쪽 모두 float32로 저장되므로 변환 손실 영향 없음. Ollama 미세 잡음으로 임계 미달 시 ≥ 0.999로 한 단계 완화.

### Phase 8 — 운영 검증 (병행 운영)
- 봇은 단일 토큰 제약으로 병행 불가 → **테스트 봇 토큰**에서 운영 시뮬레이션 (1주일)
- 워커 병행 운영 **전제 조건**: 현재 `workers/embedding_worker.py`의 `fetch_batch`에는 `FOR UPDATE SKIP LOCKED`가 없어 그대로 Python+Go 동시 실행 시 동일 batch 중복 처리(Ollama 요청 2배 + UPDATE 충돌). Phase 8 진입 전 다음 선행:
  - Python 워커의 `fetch_batch`에 `SELECT ... FOR UPDATE SKIP LOCKED` 적용
  - `fetch_batch` → `save_embeddings`를 단일 트랜잭션으로 묶기
  - Go 워커도 동일 패턴으로 직역
  - 부하 테스트(같은 DB에 Python+Go 워커 동시 기동)로 중복 처리 0건 확인
- 웹/MCP는 병행 불가 (포트 충돌, MCP client 라우팅 단일) → 시간대 분리 테스트
- 메모리·CPU·요청 지연 메트릭 비교
- **DoD:** 1주일 운영 무에러, 메모리 절감 확인, 성능 회귀 없음.

### Phase 9 — 컷오버
- `docker-compose.yml`에서 Python 서비스 제거, Go 서비스만 남김
- 운영 봇 토큰을 Go 봇으로 전환 (Python 봇 정지 → Go 봇 즉시 시작)
- RESUME 윈도우 안에 부팅 → 이벤트 무손실 (수십 초 안에 부팅 보장)
- **롤백 계획:** `docker-compose.yml`을 이전 커밋으로 되돌리고 `up -d`. DB 호환이라 즉시 복구 가능.
- **DoD:** 본 운영 환경에서 24h 안정 동작.

---

## 6. 리스크와 완화 방안

| # | 리스크 | 영향 | 가능성 | 완화 |
|---|---|---|---|---|
| R1 | 권한 계산 비트 연산 미세 버그 (`int` vs `uint64`, deny/allow 순서) | 채널이 통째로 검색 결과에서 사라짐 | 중 | Phase 1에서 Python/Go diff 테스트 100건 통과 강제 |
| R2 | SSE 보안 모델 차이 + Go 직역 시 `_session_owners` map의 data race | 타인 session 주입 차단 누락; goroutine 동시 read/write로 data race → 잘못된 user 권한 적용 | 중 | Phase 2에서 보안 시나리오 명시 + SDK의 session↔transport 묶음을 소스 레벨 확인. `_session_owners` 등가물은 `sync.RWMutex` 또는 `sync.Map`으로 보호. `go test -race` 통과 강제 |
| R3 | `pgx`의 `text[]` 바인딩이 GIN 인덱스를 안 타는 plan | `channel_access_cache` invalidate 풀스캔 → 봇 라텐시 폭증 | 중 | Phase 5/6 직후 `EXPLAIN ANALYZE`로 두 invalidate 쿼리가 GIN을 타는지 확인 |
| R4 | `[]float64` ↔ `[]float32` 변환 누락 | 워커 첫 UPDATE에서 pgvector type 에러 | 낮 | Phase 7 코드 리뷰 시 명시 변환 강제 |
| R5 | `pgxpool` `AfterConnect` 훅에 `pgvector.RegisterTypes` 누락 | 런타임 vector 직렬화 실패 | 낮 | 코드 리뷰 + 통합 테스트 |
| R6 | discordgo의 `MessageDeleteBulk`/`ThreadMembersUpdate`가 discord.py와 시그니처 다름 | 일부 이벤트 누락 | 낮 | Phase 4에서 분기 로직 명시 작성 |
| R7 | `vector(1024)` 차원 하드코딩 동기화 누락 (워커/봇/웹) | 모델 교체 시 런타임 dim mismatch | 낮 | 차원을 코드 한 곳의 const로 정의, 모든 사용처가 import |
| R8 | OAuth 자체 구현 분량 저평가 — `oauth_server.py` 355줄 + Discord OAuth 클라이언트(`auth.py`) 168줄. FastAPI Form/Depends 자동 바인딩이 사라져 실제 Go 분량 + 부가 작업이 추정치보다 큼 | 일정 초과, 직역 중 엣지 케이스 누락 | 중 | Phase 6에서 Python 1:1 직역 (재설계 금지). DoD에 `httptest` 통합 테스트로 엣지 케이스 강제 검증 |
| R9 | 봇 토큰 단일성으로 병행 운영 불가 → 컷오버 전 충분한 검증 어려움 | 본 운영에서야 발견되는 회귀 | 중 | Phase 8에서 테스트 봇 토큰 + 별 길드로 1주일 시뮬레이션 강제 |
| R10 | `_USED_JTIS` 일회성 저장소가 in-memory dict — multi-replica 배포 시 auth code 재사용 허용 | OAuth 보안 회귀 (탈취된 auth code의 재사용 가능) | 낮 | 기본 정책: **단일 인스턴스 배포 제약**을 README/SETUP에 명시. Go 직역은 `sync.Map` + 만료 goroutine으로 (단일 프로세스 가정을 코드 주석에 박기). 향후 multi-replica 필요 시 Redis 교체를 별도 작업으로 분리 |

---

## 7. 검증 전략

### 7.1 단위 테스트
- 권한 계산 (`internal/permissions`): table-driven 테스트, Discord 권한 명세 케이스 망라
- 토큰 추정 (`internal/embedding`): 한글/영문 비율별 입력
- ISO 8601 → DB 형식 변환 (`internal/mcp`): Z 접미사·offset 변형·naive datetime
- JWT 발급/검증 (`internal/auth`): exp·type·sub·jti

### 7.2 동등성 테스트 (vs Python)
- Phase 1: 권한 계산 diff (100건)
- Phase 6: MCP 4툴의 응답 diff (각 10 query)
- Phase 7: 임베딩 cosine 유사도 (≥ 0.9999)

### 7.3 통합 테스트
- 테스트 길드 + 테스트 봇 토큰
- 모든 슬래시 커맨드 호출
- 모든 이벤트(입퇴장, 밴, 채널/역할 변경, AutoMod, 예약 이벤트, 통합)를 인위적으로 트리거 → DB row 검증

### 7.4 운영 메트릭 비교
- 메모리 RSS, CPU%, 게이트웨이 reconnect 빈도, 검색 응답 지연 p50/p95
- Phase 8 1주일 데이터 수집

---

## 8. 컷오버 계획

### 8.0 병행운영 전략 (Phase 8 핵심)

컴포넌트마다 병행 가능 여부와 검증 방식이 다르다.

#### 봇 — 병행 **불가**

- Discord 게이트웨이는 토큰당 단일 IDENTIFY만 허용 → 본 봇 토큰을 두 인스턴스가 동시에 쓰면 한쪽이 `Session Replaced`로 강제 종료
- **전략**: 별도 **테스트 봇 토큰**을 발급해 별 길드(테스트 길드)에 초대. Go 봇은 테스트 환경에서, Python 봇은 본 환경에서 동시 운영하며 1주일 시뮬레이션
- 본 컷오버는 단일 시점에 토큰 swap (Phase 9)
- 검증 방법: 테스트 길드에서 같은 사용자 행동(메시지·역할 변경·AutoMod 룰)을 양쪽에 동일 시간 트리거 → DB row 비교 (Python DB와 Go DB는 분리해서 보관)

#### 웹 / MCP — 병행 **가능 (별 포트)**

- Python web: 기존 `127.0.0.1:8080`
- Go web: `127.0.0.1:8081` (또는 별 호스트명, 또는 path-based 라우팅)
- **DB는 같은 PostgreSQL 인스턴스 공유** (스키마 호환이라 OK)
- **JWT_SECRET은 같은 값 공유** → 같은 토큰으로 양쪽 검증 가능 → 동등성 테스트 가능
- 라우팅 옵션:
  - **A. 별 포트에서 수동 비교**: `curl localhost:8080/api/channels` vs `curl localhost:8081/api/channels` 응답 diff
  - **B. 리버스 프록시 split**: Nginx/Caddy로 1% 트래픽을 Go로 (canary). 응답 dual-write 후 결과 비교
  - **C. 시간대 분리**: 오전엔 Python, 오후엔 Go (간단하지만 비교 어려움)
- **MCP는 클라이언트(Claude Desktop)가 단일 URL만 보므로**, 본 운영용 MCP 엔드포인트는 한 시점에 한 쪽만. 별 도메인(`mcp-go.example.com`)으로 Go MCP 서버를 노출해 별 클라이언트 설정으로 검증

#### 워커 — 병행 **가능 (선행 조건 충족 시)**

- Python/Go 워커가 같은 `messages` 테이블을 보고 row를 분배 처리
- **선행 조건** (Must-fix [8]):
  - Python `embedding_worker.py`의 `fetch_batch`에 `SELECT ... FOR UPDATE SKIP LOCKED` 적용
  - `fetch_batch` → `save_embeddings`를 단일 트랜잭션으로 묶기
  - Go 워커도 같은 패턴
- 점진 전환: Go 워커 `CONCURRENCY`를 1부터 시작 → 1주일 무에러면 4까지 증가 → Python 워커를 0으로 stop
- 메트릭: `messages.embedding IS NULL` 카운트 시계열, 분당 처리량, Ollama 에러율을 Python/Go 양쪽 로그로 비교

#### 데이터 무결성 검증 (병행 기간)

- 권한 캐시(`channel_access_cache`): Python/Go 양쪽이 같은 user_id에 대해 같은 채널 집합을 반환해야 함. Phase 8 동안 매일 1회 sample diff
- 임베딩: 같은 메시지 100건 샘플로 cosine 유사도 측정 (Recommended [7]에서 임계 결정 후 적용)
- 메시지·이벤트 row: 봇은 병행 불가라 직접 비교 불가 → 테스트 길드에서 동일 시나리오 재현 후 row diff

#### 롤백 트리거 (Phase 8 → 7로 회귀)

다음 중 하나라도 발생 시 Go 측을 즉시 stop, Python 단독 운영으로 회귀:
- Go 워커가 같은 batch를 중복 처리 (SKIP LOCKED 동작 실패)
- 웹 검색 결과의 채널 집합이 Python과 다름 (권한 모델 회귀)
- MCP가 `httptest` 자동화 케이스 중 하나라도 fail
- 메모리/CPU가 Python 대비 회귀

### 8.1 컷오버 절차

1. 운영 환경에서 Phase 8 검증 통과 확인
2. 본 봇 토큰으로 Go 봇을 별 환경에서 30분 dry-run (게이트웨이 연결 끊지 않도록 토큰은 동시에 한 곳만)
3. 메인테넌스 윈도우 (5분):
   - `docker compose stop bot web worker` (Python)
   - `docker compose up -d` (Go 서비스만)
   - **Python stop 시각 → Go 봇 `READY` 로그 시각까지 < 10초 측정** (Discord RESUME 윈도우 안에 부팅해야 이벤트 무손실)
   - 10초 초과 시 컷오버 중단, Python 재기동 후 Go 부팅 시간 단축 작업 (이미지 슬림화, eager DB 연결 등)
   - 헬스체크 통과 확인
4. RESUME 성공 여부 로그 확인 (`Logged in as ...` 직후 sequence 누락 없음)
5. 30분 모니터링: 슬래시 커맨드, 웹 로그인, MCP 연결, 워커 처리 속도

### 8.2 롤백

- `git revert` 또는 `git checkout master` → `docker compose up -d`
- DB 호환이라 즉시 복구
- Go에서 누락된 이벤트는 Python이 다시 받아서 처리 (RESUME 또는 재시작 backfill 없음 — 일부 누락 가능, 수용)

### 8.3 컷오버 이후

- 1주일 모니터링 후 Python 코드를 별도 PR에서 정리 (디렉토리 삭제, requirements.txt 제거)
- README / docs/SETUP.md 업데이트

---

## 9. 일정

명시 일정 없음 — 각 Phase의 DoD를 만족할 때까지 진행. 우선순위는 R1·R2·R9 (가장 늦게 발견되면 비싼 리스크).

대략적 분량 가이드 (단순 직역 기준):
- Phase 0~2: 부트스트랩 + 핵심 POC (가장 위험, 가장 신중)
- Phase 3~6: 기능 직역 (분량의 70%)
- Phase 7: 단순 (워커는 ~250줄)
- Phase 8~9: 운영 검증 + 컷오버

---

## 10. 미결 사항 / 결정 필요

- [x] **로거**: `log/slog` + JSON 핸들러 사용 (의존성 0, 충분히 빠름)
- [x] **SQLite 백엔드**: 미지원 (§2.2 비목표 참조)
- [x] **임베딩 차원**: 코드 const로 한 곳에서 정의, 모든 사용처가 import (R7)
- [x] **단일 바이너리**: 미구현. 봇/웹/워커는 각각 독립 바이너리로만 빌드 (§2.2 비목표 참조)

모든 결정 사항 확정 완료.
