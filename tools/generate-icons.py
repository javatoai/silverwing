"""Generate rounded Silverwing app icons from the PNG export of app-icon.svg."""

from __future__ import annotations

import io
import struct
from pathlib import Path

from PIL import Image, ImageChops, ImageDraw, ImageOps


ROOT = Path(__file__).resolve().parents[1]
RESOURCE_DIR = ROOT / "desktop" / "src" / "main" / "resources"
COMPOSE_RESOURCE_DIR = ROOT / "desktop" / "src" / "main" / "composeResources" / "drawable"
MASTER_ICON = ROOT / "tools" / "assets" / "silverwing-icon-master.png"


def render(size: int) -> Image.Image:
    with Image.open(MASTER_ICON) as source:
        image = ImageOps.fit(source.convert("RGBA"), (size, size), Image.Resampling.LANCZOS)
    rounded = Image.new("L", (size, size), 0)
    ImageDraw.Draw(rounded).rounded_rectangle(
        (0, 0, size - 1, size - 1),
        radius=round(size * 0.22),
        fill=255,
    )
    image.putalpha(ImageChops.multiply(image.getchannel("A"), rounded))
    return image


def write_icns(images: dict[int, Image.Image], destination: Path) -> None:
    chunks = []
    for code, size in (
        (b"ic11", 32),
        (b"ic12", 64),
        (b"ic07", 128),
        (b"ic13", 256),
        (b"ic08", 256),
        (b"ic14", 512),
        (b"ic09", 512),
        (b"ic10", 1024),
    ):
        buffer = io.BytesIO()
        images[size].save(buffer, format="PNG")
        payload = buffer.getvalue()
        chunks.append(code + struct.pack(">I", len(payload) + 8) + payload)
    body = b"".join(chunks)
    destination.write_bytes(b"icns" + struct.pack(">I", len(body) + 8) + body)


def main() -> None:
    RESOURCE_DIR.mkdir(parents=True, exist_ok=True)
    COMPOSE_RESOURCE_DIR.mkdir(parents=True, exist_ok=True)
    sizes = (16, 24, 32, 48, 64, 128, 256, 512, 1024)
    images = {size: render(size) for size in sizes}
    images[512].save(RESOURCE_DIR / "app-icon.png", format="PNG")
    images[512].save(COMPOSE_RESOURCE_DIR / "app_icon.png", format="PNG")
    images[256].save(
        RESOURCE_DIR / "app-icon.ico",
        format="ICO",
        sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)],
    )
    write_icns(images, RESOURCE_DIR / "app-icon.icns")


if __name__ == "__main__":
    main()
