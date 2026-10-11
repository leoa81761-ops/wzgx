import struct
import zlib
from functools import lru_cache


def _chunk(tag: bytes, data: bytes) -> bytes:
    return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)


@lru_cache(maxsize=8)
def solid_png(rgb: tuple[int, int, int], width: int = 900, height: int = 383) -> bytes:
    row = b"\x00" + bytes(rgb) * width
    raw = row * height
    ihdr = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + _chunk(b"IHDR", ihdr) + _chunk(b"IDAT", zlib.compress(raw, 9)) + _chunk(b"IEND", b"")


COVER_COLORS = {
    "simple": (156, 59, 36),
    "fresh": (31, 106, 77),
    "business": (30, 58, 95),
}


def cover_png(template_key: str) -> bytes:
    return solid_png(COVER_COLORS.get(template_key, COVER_COLORS["simple"]))
