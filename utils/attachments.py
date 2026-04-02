"""Discord 첨부파일 및 커스텀 이모지를 로컬에 다운로드하는 유틸리티."""

import logging
import os
import re
from pathlib import Path

import aiohttp

log = logging.getLogger(__name__)

ATTACHMENTS_DIR = Path(os.getenv("ATTACHMENTS_DIR", "data/attachments"))
EMOJIS_DIR = Path(os.getenv("EMOJIS_DIR", "data/emojis"))

# <:name:id> 또는 <a:name:id> (애니메이션)
CUSTOM_EMOJI_RE = re.compile(r"<(a?):(\w+):(\d+)>")


async def _download_file(url: str, dest: Path) -> bool:
    """URL에서 파일을 다운로드한다. 성공 시 True."""
    dest.parent.mkdir(parents=True, exist_ok=True)
    try:
        async with aiohttp.ClientSession() as session:
            async with session.get(url, timeout=aiohttp.ClientTimeout(total=30)) as resp:
                if resp.status != 200:
                    log.warning("다운로드 실패 (HTTP %s): %s", resp.status, url)
                    return False
                with open(dest, "wb") as f:
                    async for chunk in resp.content.iter_chunked(8192):
                        f.write(chunk)
    except Exception:
        log.exception("다운로드 중 오류: %s", url)
        return False
    return True


async def download_attachment(
    url: str,
    channel_id: str,
    message_id: str,
    filename: str,
) -> str | None:
    """Discord CDN에서 첨부파일을 다운로드하고 로컬 상대 경로를 반환한다."""
    rel_path = f"{channel_id}/{message_id}_{filename}"
    dest = ATTACHMENTS_DIR / rel_path

    if not await _download_file(url, dest):
        return None
    return rel_path


async def download_emojis(content: str) -> None:
    """메시지 텍스트에서 커스텀 이모지를 파싱하고 미저장된 이모지를 다운로드한다."""
    for match in CUSTOM_EMOJI_RE.finditer(content):
        animated, _name, emoji_id = match.group(1), match.group(2), match.group(3)
        ext = "gif" if animated else "png"
        dest = EMOJIS_DIR / f"{emoji_id}.{ext}"

        if dest.exists():
            continue

        url = f"https://cdn.discordapp.com/emojis/{emoji_id}.{ext}"
        await _download_file(url, dest)
