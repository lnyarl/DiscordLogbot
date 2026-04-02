---
name: discord-feature-planner
description: "디스코드 봇 기능을 설계하고 구현 스펙을 작성한다. 새 기능 추가, 커맨드 설계, 이벤트 핸들러 계획, 로깅 구조 설계가 필요할 때 반드시 이 스킬을 사용할 것."
---

# Discord Feature Planner

디스코드 봇의 새 기능을 설계하고 discord-coder가 바로 구현할 수 있는 스펙 문서를 작성한다.

## 스펙 문서 구조

다음 구조로 `_workspace/01_plan_spec.md`를 작성한다:

```markdown
# 기능 스펙: [기능명]

## 가정 사항
(요구사항에서 명확하지 않아 가정한 내용을 여기에 기록)

## 기술 스택
- 언어: Python 3.10+ / JavaScript (Node.js 18+)
- 라이브러리: discord.py 2.x / discord.js 14.x
- 저장소: 없음 / SQLite / PostgreSQL / JSON 파일

## 파일/모듈 구조
```
project/
├── bot.py (또는 index.js)  # 메인 엔트리포인트
├── cogs/                   # 기능 모듈 (discord.py Cog)
│   └── logging.py
├── utils/
│   └── log_formatter.py
├── .env                    # 환경변수
└── requirements.txt
```

## 커맨드 목록

| 커맨드 | 타입 | 설명 | 파라미터 | 권한 |
|--------|------|------|----------|------|
| /setlog | 슬래시 | 로그 채널 설정 | channel: TextChannel | MANAGE_GUILD |

## 이벤트 핸들러

| 이벤트 | 트리거 | 처리 내용 | 로그 형식 |
|--------|--------|-----------|-----------|
| on_message_delete | 메시지 삭제 | 삭제된 메시지 내용, 작성자, 채널 로깅 | `[DELETE] #채널 @유저: 내용` |
| on_message_edit | 메시지 수정 | 수정 전/후 내용 로깅 | `[EDIT] #채널 @유저: 이전→이후` |

## 로그 포맷

각 로그 항목은 다음 정보를 포함한다:
- 타임스탬프 (UTC, ISO 8601)
- 이벤트 타입
- 대상 사용자 (ID + 닉네임)
- 채널/서버 정보
- 상세 내용

## Intents 요구사항
- `message_content`: 메시지 내용 접근 (Privileged)
- `members`: 멤버 입퇴장 감지
- `guilds`: 기본 서버 정보

## 주의사항 / 엣지케이스
- 봇 자신의 메시지는 로깅에서 제외
- 메시지가 2000자 초과 시 분할 또는 파일 첨부로 처리
- 채널이 삭제된 경우 ID만 기록
```

## 로깅 봇 특화 설계 패턴

### 로그 채널 구조
서버당 로그 채널을 설정 가능하게 한다. 설정은 JSON 파일 또는 DB에 `guild_id: channel_id` 형태로 저장한다.

### 민감 정보 처리
DM 채널 메시지는 기본적으로 로깅하지 않는다. NSFW 채널 메시지는 별도 플래그로 관리한다.

### 대용량 메시지
Discord 메시지 한도(2000자)를 초과하는 로그는 파일 업로드 또는 분할 전송으로 처리한다.

## 출력

완성된 스펙 문서를 `_workspace/01_plan_spec.md`에 저장하고, discord-coder에게 SendMessage로 알린다.
