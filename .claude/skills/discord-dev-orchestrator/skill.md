---
name: discord-dev-orchestrator
description: "디스코드 봇 개발 팀(플래너, 코더, 리뷰어)을 조율하는 오케스트레이터. 새 기능을 처음부터 끝까지 개발하거나, 봇 개발 작업을 팀으로 진행하고 싶을 때 반드시 이 스킬을 사용할 것."
---

# Discord Dev Orchestrator

Discord 봇 개발 팀을 조율하여 기능 설계부터 구현, 리뷰까지 완전한 개발 사이클을 실행한다.

## 실행 모드: 에이전트 팀

## 에이전트 구성

| 팀원 | 에이전트 타입 | 역할 | 스킬 | 출력 |
|------|-------------|------|------|------|
| discord-planner | discord-planner | 기능 설계, 스펙 작성 | discord-feature-planner | `_workspace/01_plan_spec.md` |
| discord-coder | discord-coder | 코드 구현 | discord-bot-coder | 소스 파일 + `_workspace/02_code_summary.md` |
| discord-reviewer | discord-reviewer | 코드 리뷰, 검증 | discord-code-reviewer | `_workspace/03_review_report.md` |

## 워크플로우

### Phase 1: 준비

1. 사용자 요구사항 파악:
   - 어떤 기능인가?
   - 기술 스택 지정이 있는가? (없으면 discord.py + Python)
   - 기존 코드가 있는가? (프로젝트 루트 탐색)
2. `_workspace/` 디렉토리 생성
3. 요구사항 요약을 `_workspace/00_input/requirements.md`에 저장

### Phase 2: 팀 구성

```
TeamCreate(
  team_name: "discord-dev-team",
  members: [
    {
      name: "discord-planner",
      agent_type: "discord-planner",
      model: "opus",
      prompt: "당신은 discord-planner입니다. Skill 도구로 discord-feature-planner 스킬을 호출하여
               _workspace/00_input/requirements.md의 요구사항을 분석하고 구현 스펙을 작성하세요.
               완료 후 discord-coder에게 SendMessage로 알려주세요."
    },
    {
      name: "discord-coder",
      agent_type: "discord-coder",
      model: "opus",
      prompt: "당신은 discord-coder입니다. discord-planner로부터 스펙 완료 알림을 받으면
               Skill 도구로 discord-bot-coder 스킬을 호출하여 코드를 구현하세요.
               완료 후 discord-reviewer에게 SendMessage로 알려주세요."
    },
    {
      name: "discord-reviewer",
      agent_type: "discord-reviewer",
      model: "opus",
      prompt: "당신은 discord-reviewer입니다. discord-coder로부터 구현 완료 알림을 받으면
               Skill 도구로 discord-code-reviewer 스킬을 호출하여 코드를 리뷰하세요.
               완료 후 리더에게 SendMessage로 리뷰 결과를 알려주세요."
    }
  ]
)
```

작업 등록:
```
TaskCreate(tasks: [
  { title: "기능 스펙 작성", description: "requirements.md 기반 구현 스펙 작성", assignee: "discord-planner" },
  { title: "코드 구현", description: "스펙 기반 봇 코드 작성", assignee: "discord-coder", depends_on: ["기능 스펙 작성"] },
  { title: "코드 리뷰", description: "구현된 코드 리뷰 및 검증", assignee: "discord-reviewer", depends_on: ["코드 구현"] }
])
```

### Phase 3: 개발 사이클

**실행 방식:** 팀원들이 파이프라인으로 자체 조율

순서:
1. discord-planner가 스펙 작성 → discord-coder에게 알림
2. discord-coder가 코드 구현 → discord-reviewer에게 알림
3. discord-reviewer가 리뷰 → CRITICAL 발견 시 discord-coder에게 즉시 알림

**수정 루프:** CRITICAL/WARNING 발견 시 discord-coder가 수정 후 discord-reviewer가 재리뷰 (최대 2회)

**산출물 저장:**

| 팀원 | 출력 경로 |
|------|----------|
| discord-planner | `_workspace/01_plan_spec.md` |
| discord-coder | 프로젝트 소스 파일 + `_workspace/02_code_summary.md` |
| discord-reviewer | `_workspace/03_review_report.md` |

### Phase 4: 통합 및 완료

1. `_workspace/03_review_report.md` 읽어 최종 상태 확인
2. APPROVED이면 완료 보고
3. REQUIRES_FIX이면 미해결 항목 사용자에게 알림
4. 생성된 파일 목록 사용자에게 요약 보고

### Phase 5: 정리

1. 팀원들에게 종료 신호 SendMessage
2. TeamDelete로 팀 정리
3. `_workspace/` 보존 (삭제하지 않음)
4. 사용자에게 최종 결과 요약

## 데이터 흐름

```
요구사항 → [discord-planner] → 01_plan_spec.md
                                      ↓
                              [discord-coder] → 소스 파일 + 02_code_summary.md
                                                              ↓
                                                   [discord-reviewer] → 03_review_report.md
                                                                              ↓
                                                                      [리더: 통합 보고]
```

## 에러 핸들링

| 상황 | 전략 |
|------|------|
| discord-planner 실패 | 리더가 직접 기본 스펙 작성 후 discord-coder에게 전달 |
| discord-coder 실패 | 1회 재시작. 재실패 시 사용자에게 알리고 부분 구현 내용 보고 |
| discord-reviewer 실패 | 1회 재시작. 재실패 시 리뷰 없이 코드만 제공하고 사용자에게 수동 리뷰 권고 |
| 수정 루프 2회 초과 | 미해결 항목 목록화하여 사용자에게 전달, 수동 처리 요청 |

## 테스트 시나리오

### 정상 흐름
1. 사용자: "멤버 입퇴장 로깅 기능 추가해줘"
2. Phase 1: `_workspace/00_input/requirements.md` 생성
3. Phase 2: 3명 팀 구성, 작업 3개 등록
4. Phase 3:
   - discord-planner: 이벤트 핸들러 스펙 작성 → discord-coder 알림
   - discord-coder: `cogs/logging.py`에 `on_member_join/leave` 구현 → discord-reviewer 알림
   - discord-reviewer: 보안/기능 리뷰 → APPROVED
5. Phase 4: 생성 파일 목록 보고
6. 예상 결과: `cogs/logging.py` 생성, `_workspace/` 3개 파일 보존

### 에러 흐름
1. Phase 3에서 discord-reviewer가 `[CRITICAL] 토큰 하드코딩` 발견
2. discord-coder에게 즉시 SendMessage
3. discord-coder가 `.env` 처리로 수정 후 재구현
4. discord-reviewer 재리뷰 → APPROVED
5. 수정 이력이 `_workspace/`에 보존됨
