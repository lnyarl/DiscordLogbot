"""Phase 1 동등성 fixture 생성기.

`web/permissions.py`의 can_view_channel을 다양한 입력에 대해 호출해
internal/permissions/testdata/fixtures.json 을 만든다.

Go 측 fixtures_test.go가 같은 JSON을 로드해 같은 결과를 내는지 검증한다.
양쪽이 모두 같은 기댓값과 일치해야 동등성 입증.

실행:
    .venv/Scripts/python tools/gen_permission_fixtures.py
"""
from __future__ import annotations

import json
import os
import sys
from itertools import product

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

from web.permissions import can_view_channel, VIEW_CHANNEL, ADMINISTRATOR

GUILD_ID = "100"
USER_ID = "200"

VIEW = str(VIEW_CHANNEL)
ADMIN = str(ADMINISTRATOR)
COMBO = str(VIEW_CHANNEL | ADMINISTRATOR)
ZERO = "0"


def make(name, guild, member_roles, channel, categories=None):
    categories = categories or {}
    expected = can_view_channel(
        channel, set(member_roles), guild, USER_ID, GUILD_ID, categories,
    )
    return {
        "name": name,
        "input": {
            "guild": guild,
            "member_roles": list(member_roles),
            "channel": channel,
            "categories": categories,
            "user_id": USER_ID,
            "guild_id": GUILD_ID,
        },
        "expected": bool(expected),
    }


def base_guild(everyone_perms=ZERO, extra_roles=()):
    roles = [{"id": GUILD_ID, "permissions": everyone_perms}]
    roles.extend(extra_roles)
    return {"owner_id": "owner-id", "roles": roles}


def ow(target_id, allow=ZERO, deny=ZERO, type=0):
    return {"id": target_id, "type": type, "allow": allow, "deny": deny}


fixtures = []


# ── 기본 케이스 ─────────────────────────────────────────────────────────────
fixtures.append(make(
    "owner always sees",
    guild={"owner_id": USER_ID, "roles": [{"id": GUILD_ID, "permissions": ZERO}]},
    member_roles=[],
    channel={"id": "1", "type": 0},
))
fixtures.append(make(
    "everyone has VIEW",
    guild=base_guild(everyone_perms=VIEW),
    member_roles=[],
    channel={"id": "1", "type": 0},
))
fixtures.append(make(
    "everyone has nothing",
    guild=base_guild(everyone_perms=ZERO),
    member_roles=[],
    channel={"id": "1", "type": 0},
))


# ── @everyone overwrite ────────────────────────────────────────────────────
fixtures.append(make(
    "everyone overwrite deny",
    guild=base_guild(everyone_perms=VIEW),
    member_roles=[],
    channel={"id": "1", "type": 0, "permission_overwrites": [ow(GUILD_ID, deny=VIEW)]},
))
fixtures.append(make(
    "everyone overwrite allow",
    guild=base_guild(everyone_perms=ZERO),
    member_roles=[],
    channel={"id": "1", "type": 0, "permission_overwrites": [ow(GUILD_ID, allow=VIEW)]},
))
fixtures.append(make(
    "everyone allow+deny same bit (allow wins per-overwrite)",
    guild=base_guild(everyone_perms=VIEW),
    member_roles=[],
    channel={"id": "1", "type": 0, "permission_overwrites": [ow(GUILD_ID, allow=VIEW, deny=VIEW)]},
))


# ── 역할 권한 (server-level) ────────────────────────────────────────────────
fixtures.append(make(
    "role grants VIEW at server level",
    guild=base_guild(everyone_perms=ZERO, extra_roles=[
        {"id": "role-a", "permissions": VIEW},
    ]),
    member_roles=["role-a"],
    channel={"id": "1", "type": 0},
))
fixtures.append(make(
    "ADMIN role bypasses channel deny",
    guild=base_guild(everyone_perms=ZERO, extra_roles=[
        {"id": "role-a", "permissions": ADMIN},
    ]),
    member_roles=["role-a"],
    channel={"id": "1", "type": 0, "permission_overwrites": [ow(GUILD_ID, deny=VIEW)]},
))
fixtures.append(make(
    "ADMIN role + nothing else",
    guild=base_guild(everyone_perms=ZERO, extra_roles=[
        {"id": "role-a", "permissions": ADMIN},
    ]),
    member_roles=["role-a"],
    channel={"id": "1", "type": 0},
))
fixtures.append(make(
    "user not in admin role",
    guild=base_guild(everyone_perms=ZERO, extra_roles=[
        {"id": "role-a", "permissions": ADMIN},
    ]),
    member_roles=[],
    channel={"id": "1", "type": 0},
))


# ── 역할 overwrites — deny / allow / 두 역할 충돌 ──────────────────────────
fixtures.append(make(
    "role overwrite allow over everyone deny",
    guild=base_guild(everyone_perms=ZERO, extra_roles=[
        {"id": "role-a", "permissions": ZERO},
    ]),
    member_roles=["role-a"],
    channel={"id": "1", "type": 0, "permission_overwrites": [
        ow(GUILD_ID, deny=VIEW),
        ow("role-a", allow=VIEW),
    ]},
))
fixtures.append(make(
    "role overwrite deny over everyone allow",
    guild=base_guild(everyone_perms=VIEW, extra_roles=[
        {"id": "role-a", "permissions": ZERO},
    ]),
    member_roles=["role-a"],
    channel={"id": "1", "type": 0, "permission_overwrites": [ow("role-a", deny=VIEW)]},
))
fixtures.append(make(
    "two roles: deny + allow on same bit (allow batch wins)",
    guild=base_guild(everyone_perms=VIEW, extra_roles=[
        {"id": "r-deny", "permissions": ZERO},
        {"id": "r-allow", "permissions": ZERO},
    ]),
    member_roles=["r-deny", "r-allow"],
    channel={"id": "1", "type": 0, "permission_overwrites": [
        ow("r-deny", deny=VIEW),
        ow("r-allow", allow=VIEW),
    ]},
))
fixtures.append(make(
    "two roles deny only → false",
    guild=base_guild(everyone_perms=VIEW, extra_roles=[
        {"id": "r1", "permissions": ZERO},
        {"id": "r2", "permissions": ZERO},
    ]),
    member_roles=["r1", "r2"],
    channel={"id": "1", "type": 0, "permission_overwrites": [
        ow("r1", deny=VIEW),
        ow("r2", deny=VIEW),
    ]},
))
fixtures.append(make(
    "user not in role with overwrite → overwrite ignored",
    guild=base_guild(everyone_perms=VIEW, extra_roles=[
        {"id": "role-a", "permissions": ZERO},
    ]),
    member_roles=[],
    channel={"id": "1", "type": 0, "permission_overwrites": [ow("role-a", deny=VIEW)]},
))


# ── 멤버 개인 overwrite ────────────────────────────────────────────────────
fixtures.append(make(
    "member deny over role allow",
    guild=base_guild(everyone_perms=VIEW, extra_roles=[
        {"id": "role-a", "permissions": ZERO},
    ]),
    member_roles=["role-a"],
    channel={"id": "1", "type": 0, "permission_overwrites": [
        ow("role-a", allow=VIEW),
        ow(USER_ID, deny=VIEW, type=1),
    ]},
))
fixtures.append(make(
    "member allow over role deny",
    guild=base_guild(everyone_perms=VIEW, extra_roles=[
        {"id": "role-a", "permissions": ZERO},
    ]),
    member_roles=["role-a"],
    channel={"id": "1", "type": 0, "permission_overwrites": [
        ow("role-a", deny=VIEW),
        ow(USER_ID, allow=VIEW, type=1),
    ]},
))
fixtures.append(make(
    "member allow over everyone deny",
    guild=base_guild(everyone_perms=ZERO),
    member_roles=[],
    channel={"id": "1", "type": 0, "permission_overwrites": [
        ow(GUILD_ID, deny=VIEW),
        ow(USER_ID, allow=VIEW, type=1),
    ]},
))


# ── 카테고리 상속 / override ─────────────────────────────────────────────────
fixtures.append(make(
    "category deny inherits to channel",
    guild=base_guild(everyone_perms=VIEW),
    member_roles=[],
    channel={"id": "1", "type": 0, "parent_id": "cat-1"},
    categories={"cat-1": {"id": "cat-1", "type": 4, "permission_overwrites": [
        ow(GUILD_ID, deny=VIEW),
    ]}},
))
fixtures.append(make(
    "category allow inherits to channel",
    guild=base_guild(everyone_perms=ZERO),
    member_roles=[],
    channel={"id": "1", "type": 0, "parent_id": "cat-1"},
    categories={"cat-1": {"id": "cat-1", "type": 4, "permission_overwrites": [
        ow(GUILD_ID, allow=VIEW),
    ]}},
))
fixtures.append(make(
    "channel allow overrides category deny",
    guild=base_guild(everyone_perms=VIEW),
    member_roles=[],
    channel={"id": "1", "type": 0, "parent_id": "cat-1", "permission_overwrites": [
        ow(GUILD_ID, allow=VIEW),
    ]},
    categories={"cat-1": {"id": "cat-1", "type": 4, "permission_overwrites": [
        ow(GUILD_ID, deny=VIEW),
    ]}},
))
fixtures.append(make(
    "channel deny overrides category allow",
    guild=base_guild(everyone_perms=ZERO),
    member_roles=[],
    channel={"id": "1", "type": 0, "parent_id": "cat-1", "permission_overwrites": [
        ow(GUILD_ID, deny=VIEW),
    ]},
    categories={"cat-1": {"id": "cat-1", "type": 4, "permission_overwrites": [
        ow(GUILD_ID, allow=VIEW),
    ]}},
))
fixtures.append(make(
    "missing parent_id (orphan channel)",
    guild=base_guild(everyone_perms=VIEW),
    member_roles=[],
    channel={"id": "1", "type": 0, "parent_id": "ghost"},
    categories={},
))
fixtures.append(make(
    "category role overwrite + member channel overwrite",
    guild=base_guild(everyone_perms=ZERO, extra_roles=[
        {"id": "role-a", "permissions": ZERO},
    ]),
    member_roles=["role-a"],
    channel={"id": "1", "type": 0, "parent_id": "cat-1", "permission_overwrites": [
        ow(USER_ID, allow=VIEW, type=1),
    ]},
    categories={"cat-1": {"id": "cat-1", "type": 4, "permission_overwrites": [
        ow("role-a", deny=VIEW),
    ]}},
))


# ── 결합 비트 (VIEW + ADMIN as one perm value) ──────────────────────────────
fixtures.append(make(
    "everyone has VIEW+ADMIN combined permission integer",
    guild=base_guild(everyone_perms=COMBO),
    member_roles=[],
    channel={"id": "1", "type": 0, "permission_overwrites": [ow(GUILD_ID, deny=VIEW)]},
))


# ── 자동 매트릭스: @everyone perm × overwrite (allow/deny) ─────────────────
for everyone_perm, ow_allow, ow_deny in product(
    [ZERO, VIEW], [ZERO, VIEW], [ZERO, VIEW],
):
    fixtures.append(make(
        f"matrix everyone={everyone_perm} ow_allow={ow_allow} ow_deny={ow_deny}",
        guild=base_guild(everyone_perms=everyone_perm),
        member_roles=[],
        channel={"id": "1", "type": 0, "permission_overwrites": [
            ow(GUILD_ID, allow=ow_allow, deny=ow_deny),
        ]},
    ))


# ── 자동 매트릭스: 역할 + 멤버 individual overwrite ─────────────────────────
for ra_allow, ra_deny, mem_allow, mem_deny in product(
    [ZERO, VIEW], [ZERO, VIEW], [ZERO, VIEW], [ZERO, VIEW],
):
    overwrites = [ow("role-a", allow=ra_allow, deny=ra_deny)]
    if mem_allow != ZERO or mem_deny != ZERO:
        overwrites.append(ow(USER_ID, allow=mem_allow, deny=mem_deny, type=1))
    fixtures.append(make(
        f"matrix role(a={ra_allow}/d={ra_deny}) mem(a={mem_allow}/d={mem_deny})",
        guild=base_guild(everyone_perms=ZERO, extra_roles=[
            {"id": "role-a", "permissions": ZERO},
        ]),
        member_roles=["role-a"],
        channel={"id": "1", "type": 0, "permission_overwrites": overwrites},
    ))


OUTPUT = os.path.join(ROOT, "internal", "permissions", "testdata", "fixtures.json")
os.makedirs(os.path.dirname(OUTPUT), exist_ok=True)
with open(OUTPUT, "w", encoding="utf-8") as f:
    json.dump(fixtures, f, indent=2, ensure_ascii=False)

print(f"Generated {len(fixtures)} fixtures → {OUTPUT}")
