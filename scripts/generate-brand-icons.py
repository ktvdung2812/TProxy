#!/usr/bin/env python3
"""Generate tproxy brand icons (hub mark on green gradient) for tray / PWA / Electron.

Matches the dashboard brand-mark: Material 'hub' on brand-500→brand-700 gradient.
No third-party deps (stdlib only).
"""
from __future__ import annotations

import math
import struct
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def write_png(path: Path, w: int, h: int, rgba_fn) -> None:
    rows = []
    for y in range(h):
        row = bytearray([0])
        for x in range(w):
            r, g, b, a = rgba_fn(x, y, w, h)
            row.extend([r, g, b, a])
        rows.append(bytes(row))
    raw = b"".join(rows)

    def chunk(tag: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    ihdr = struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0)
    png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b"")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(png)


def lerp(a: int, b: int, t: float) -> int:
    return int(a + (b - a) * t)


def brand_pixel(x: int, y: int, w: int, h: int) -> tuple[int, int, int, int]:
    pad = max(1, w // 16)
    ix, iy = x - pad, y - pad
    iw, ih = w - 2 * pad, h - 2 * pad
    if ix < 0 or iy < 0 or ix >= iw or iy >= ih:
        return (0, 0, 0, 0)

    radius = max(2, int(iw * 0.22))
    t = (ix + iy) / (iw + ih)
    # brand-500 #3da066 → brand-700 #296b45
    r = lerp(0x3D, 0x29, t)
    g = lerp(0xA0, 0x6B, t)
    b = lerp(0x66, 0x45, t)

    def dist_to_round_rect(px: float, py: float) -> float:
        cx = min(max(px, radius), iw - 1 - radius)
        cy = min(max(py, radius), ih - 1 - radius)
        if radius <= px <= iw - 1 - radius and radius <= py <= ih - 1 - radius:
            return -min(px, py, iw - 1 - px, ih - 1 - py)
        return math.hypot(px - cx, py - cy) - radius

    d = dist_to_round_rect(ix + 0.5, iy + 0.5)
    if d > 1.2:
        return (0, 0, 0, 0)
    alpha = 255 if d < -0.5 else int(max(0, min(255, (1.2 - d) / 1.7 * 255)))

    cx, cy = iw / 2, ih / 2
    scale = iw / 36.0
    nodes = [(0, 0), (-8, -8), (8, -8), (-8, 8), (8, 8)]
    spokes = [(0, 1), (0, 2), (0, 3), (0, 4)]
    px, py = ix + 0.5, iy + 0.5
    white = 0.0
    for a, b_idx in spokes:
        x0 = cx + nodes[a][0] * scale
        y0 = cy + nodes[a][1] * scale
        x1 = cx + nodes[b_idx][0] * scale
        y1 = cy + nodes[b_idx][1] * scale
        vx, vy = x1 - x0, y1 - y0
        l2 = vx * vx + vy * vy or 1.0
        tseg = max(0.0, min(1.0, ((px - x0) * vx + (py - y0) * vy) / l2))
        dist = math.hypot(px - (x0 + tseg * vx), py - (y0 + tseg * vy))
        thickness = 1.35 * scale
        if dist < thickness + 0.8:
            white = max(white, max(0.0, 1.0 - dist / (thickness + 0.8)))
    for i, (nx, ny) in enumerate(nodes):
        nxp = cx + nx * scale
        nyp = cy + ny * scale
        rad = (3.2 if i == 0 else 2.4) * scale
        dist = math.hypot(px - nxp, py - nyp)
        if dist < rad + 0.8:
            white = max(white, max(0.0, 1.0 - max(0.0, dist - rad) / 0.8))

    if white > 0:
        return (
            int(r * (1 - white) + 255 * white),
            int(g * (1 - white) + 255 * white),
            int(b * (1 - white) + 255 * white),
            alpha,
        )
    return (r, g, b, alpha)


def write_ico(path: Path, sizes: tuple[int, ...] = (16, 32, 48, 64)) -> None:
    images: list[tuple[int, bytes]] = []
    for s in sizes:

        def chunk(tag: bytes, data: bytes) -> bytes:
            return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

        rows = []
        for y in range(s):
            row = bytearray([0])
            for x in range(s):
                r, g, b, a = brand_pixel(x, y, s, s)
                row.extend([r, g, b, a])
            rows.append(bytes(row))
        raw = b"".join(rows)
        ihdr = struct.pack(">IIBBBBB", s, s, 8, 6, 0, 0, 0)
        png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b"")
        images.append((s, png))

    count = len(images)
    header = struct.pack("<HHH", 0, 1, count)
    offset = 6 + 16 * count
    entries = b""
    data = b""
    for s, png in images:
        w = 0 if s >= 256 else s
        h = 0 if s >= 256 else s
        entries += struct.pack("<BBBBHHII", w, h, 0, 0, 1, 32, len(png), offset + len(data))
        data += png
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(header + entries + data)


def main() -> None:
    targets = [
        (ROOT / "npm/lib/tray/icon.png", 64),  # primary tray (retina-friendly)
        (ROOT / "npm/lib/tray/icon-32.png", 32),
        (ROOT / "npm/lib/tray/icon-64.png", 64),
        (ROOT / "npm/lib/tray/icon-128.png", 128),
        (ROOT / "npm/lib/tray/icon-256.png", 256),
        (ROOT / "electron/icon.png", 128),
        (ROOT / "web/public/icon-192.png", 192),
        (ROOT / "web/public/icon-512.png", 512),
        (ROOT / "internal/api/dashboard/icon-192.png", 192),
        (ROOT / "internal/api/dashboard/icon-512.png", 512),
    ]
    for path, size in targets:
        write_png(path, size, size, brand_pixel)
        print(f"wrote {path.relative_to(ROOT)} ({size}x{size})")
    write_ico(ROOT / "npm/lib/tray/icon.ico")
    print("wrote npm/lib/tray/icon.ico")
    write_ico(ROOT / "electron/icon.ico", sizes=(16, 32, 48, 64, 128, 256))
    print("wrote electron/icon.ico")


if __name__ == "__main__":
    main()
