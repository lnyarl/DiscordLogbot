---
name: discord-code-reviewer
description: "디스코드 봇 코드를 리뷰하고 검증한다. 코드 리뷰, 버그 탐색, 보안 점검, 스펙 준수 여부 확인이 필요하면 반드시 이 스킬을 사용할 것."
---

# Discord Code Reviewer

구현된 코드를 스펙과 교차 비교하여 버그, 보안 취약점, 누락 기능을 발견한다.

## 리뷰 순서

1. `_workspace/01_plan_spec.md` 읽기 (기준 문서)
2. `_workspace/02_code_summary.md` 읽기 (구현 요약)
3. 소스 파일들 Read로 읽기 (추측하지 않는다)
4. 체크리스트 실행
5. `_workspace/03_review_report.md` 작성

## Discord 봇 보안 체크리스트

### 필수 (CRITICAL 기준)
- [ ] 봇 토큰이 소스코드에 하드코딩되어 있지 않다
- [ ] `.env` 파일이 `.gitignore`에 포함되어 있다
- [ ] 관리자 커맨드에 권한 검사가 있다 (`default_permissions` 또는 수동 체크)
- [ ] `config.json` 등 설정 파일의 경로가 공개 디렉토리가 아니다

### 기능 정확성 (WARNING 기준)
- [ ] 스펙의 모든 커맨드가 구현되어 있다
- [ ] 스펙의 모든 이벤트 핸들러가 구현되어 있다
- [ ] 봇 자신의 메시지를 로깅에서 제외하는 필터가 있다
- [ ] 로그 채널이 설정되지 않은 경우 조용히 무시한다 (에러 발생 금지)
- [ ] 메시지 2000자 초과 처리가 있다
- [ ] Privileged Intents (`message_content`, `members`)가 필요한 경우 코드에 명시되어 있다

### Discord API 모범 사례 (SUGGESTION 기준)
- [ ] `on_ready` 이후 슬래시 커맨드 sync가 있다
- [ ] 에러 핸들러 (`on_command_error`, `on_error`)가 있다
- [ ] 비동기 파일 I/O 또는 블로킹 작업을 적절히 처리한다
- [ ] Embed 사용으로 로그가 가독성 있게 표시된다

## Severity 정의

| 수준 | 의미 | 처리 |
|------|------|------|
| `[CRITICAL]` | 보안 취약점, 봇이 시작되지 않거나 데이터 손실 가능 | 즉시 discord-coder에게 SendMessage |
| `[WARNING]` | 스펙 미준수, 런타임 에러 가능성, 기능 누락 | 리포트에 기록, 수정 권고 |
| `[SUGGESTION]` | 코드 품질, 모범 사례, 개선 아이디어 | 리포트에 기록 (선택적 반영) |

## 스펙-코드 교차 검증 방법

스펙의 커맨드 목록을 순회하며 각 커맨드에 대해:
1. 코드에서 해당 커맨드명으로 Grep
2. 구현 여부 확인
3. 파라미터, 권한 설정이 스펙과 일치하는지 확인

이벤트 핸들러도 동일하게 교차 검증한다.

## 리포트 형식

`_workspace/03_review_report.md`에 저장:

```markdown
# 코드 리뷰 리포트

## 요약
- 검토 파일: N개
- 발견 사항: CRITICAL X개, WARNING Y개, SUGGESTION Z개
- 스펙 준수율: N/M 항목 (커맨드 N개 중 M개 구현)

## 스펙-코드 교차 검증

| 항목 | 스펙 | 구현 여부 | 비고 |
|------|------|-----------|------|
| /setlog | ✓ | ✓ | 정상 |
| on_message_delete | ✓ | ✗ | 미구현 |

## 발견 사항

### [CRITICAL]
- **토큰 노출**: `bot.py:5`에서 토큰이 하드코딩됨

### [WARNING]
- **권한 검사 누락**: `/setlog` 커맨드에 관리자 권한 검사 없음

### [SUGGESTION]
- Embed에 guild 아이콘을 thumbnail로 추가하면 가독성 향상

## 승인 여부

APPROVED / REQUIRES_FIX (이유)
```

## 완료 후

리뷰 완료 후 리더에게 SendMessage로 결과 알림. CRITICAL이 있으면 discord-coder에게도 즉시 전달.
