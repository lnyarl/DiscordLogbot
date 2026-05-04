# 배포 스크립트

운영 중인 Python 봇/웹/워커를 그대로 두고 **Go 측 web/worker 만 별도로**
띄우거나, 혹은 **Python + Go 를 한꺼번에** 재배포할 수 있는 통합 스크립트.

## 한눈에

| 명령 | 의미 |
|---|---|
| `scripts/up.sh` | 기본값 = `go` — Go web/worker 만 기동 |
| `scripts/up.sh go` | 동일. Go web (:8090) + Go worker, Python 무영향 |
| `scripts/up.sh all` | Python 봇/웹/워커 + Go web/worker 전체 기동 |
| `scripts/up.sh py` | Python 만 |
| `scripts/up.sh <T> --build` | 빌드 후 기동 |
| `scripts/rebuild.sh <T>` | 이미지 재빌드 + `--force-recreate` 재기동 |
| `scripts/down.sh <T>` | 정지 (컨테이너 보존, 다음 up 빠름) |
| `scripts/down.sh <T> --rm` | 컨테이너 삭제까지 |
| `scripts/logs.sh <T> [svc...]` | 로그 follow |
| `scripts/status.sh` | 양쪽 컨테이너 + 헬스 + 임베딩 큐 깊이 + 처리량 |

`<T>` 는 `go` (기본) / `all` / `py` 셋 중 하나.

## 시나리오

### A. Go 쪽만 검증
운영 Python 봇은 그대로, Go web/worker 추가 띄워서 디자인/검색 결과/임베딩
처리 동작 확인.

```bash
scripts/up.sh go --build       # 첫 배포
scripts/status.sh              # 상태 확인
scripts/logs.sh go             # 로그
scripts/down.sh go             # 끝나면 정지
```

### B. Go 코드 변경 후 무중단 재배포

```bash
scripts/rebuild.sh go
```

### C. 한꺼번에 전체 재배포

```bash
scripts/rebuild.sh all
```

`docker-compose.yml` + `docker-compose.go.yml` 양쪽 모두 적용해 Python 측
서비스도 새 빌드로 함께 갈아끼운다.

### D. Go 측만 정리

```bash
scripts/down.sh go --rm        # 컨테이너 삭제 (이미지/볼륨 보존)
```

## 포트 매핑

| 서비스 | Python 측 | Go 측 |
|---|---|---|
| web | `127.0.0.1:8080` (운영) | `127.0.0.1:8090` (검증) |
| worker `/health` | `:8082` | `:8083` |
| 봇 게이트웨이 | (Discord WSS) | **불가** — 토큰 충돌 |

같은 도메인 카나리 라우팅이 필요하면 reverse proxy(nginx/Cloudflare)에서
path/헤더 기반으로 `:8080` ↔ `:8090` 분기. JWT 서명키가 같으면 세션 쿠키
호환.

## 봇이 Go 쪽에서 빠진 이유

Discord 게이트웨이는 토큰당 세션이 유일하다. `bot-go` 를 운영 토큰으로
띄우면 Python 봇 세션이 즉시 끊긴다. 별도 테스트 봇 토큰으로만 활성화:

```bash
docker compose -f docker-compose.yml -f docker-compose.go.yml \
    --profile bot-go up -d bot-go
```

운영 검증 시뮬레이션은 별도 길드에서 진행.

## 워커 동시 실행이 안전한 이유

Phase 7 의 Go 워커는 `fetch_batch` 가 `FOR UPDATE SKIP LOCKED` 를 잡고
fetch → embed → save 를 단일 트랜잭션으로 처리한다. Python 워커도 같은
패턴으로 보강한 뒤 양쪽이 같은 DB 큐에서 동시에 row 를 빨아들여도
중복 임베딩이 발생하지 않는다.

선행 조건이 필요하면 `docs/MIGRATION-GO.md` §8 의 "워커 병행 운영 전제 조건"
체크리스트 참고.

## 디자인 일관성

`docker-compose.go.yml` 이 `./web/static:/app/web/static:ro` 로 마운트하므로
Go web 은 Python web 과 **동일한 CSS/favicon** 을 서빙한다. CSS 한 번 바꾸면
양쪽이 같이 바뀜.

## 정리

| 목적 | 명령 |
|---|---|
| 컨테이너만 stop (다음 up 빠름) | `scripts/down.sh <T>` |
| 컨테이너 삭제 (이미지·볼륨 유지) | `scripts/down.sh <T> --rm` |
| 이미지·네트워크까지 정리 | `docker compose -f docker-compose.yml -f docker-compose.go.yml down --rmi local` |

## 트러블슈팅

- `❌ docker not found in PATH` — Docker Desktop 또는 Docker Engine 설치/실행 확인
- `unknown target` — 첫 인자는 `go` / `all` / `py` 중 하나
- Windows에서 `bash` 미설치 시 — Git Bash 또는 WSL 에서 실행
