"""Generate deterministic app icons from the silverwing vector geometry."""

from __future__ import annotations

import io
import struct
from pathlib import Path

from PIL import Image, ImageDraw, ImageOps


ROOT = Path(__file__).resolve().parents[1]
RESOURCE_DIR = ROOT / "desktop" / "src" / "main" / "resources"
COMPOSE_RESOURCE_DIR = ROOT / "desktop" / "src" / "main" / "composeResources" / "drawable"


def render(size: int) -> Image.Image:
    scale = 4
    canvas = size * scale
    image = Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0))
    margin = int(canvas * 0.0625)
    radius = int(canvas * 0.2265)
    gradient = Image.linear_gradient("L").resize((canvas, canvas))
    gradient = ImageOps.colorize(gradient, "#3B82F6", "#1D4ED8").convert("RGBA")
    mask = Image.new("L", (canvas, canvas), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        (margin, margin, canvas - margin, canvas - margin),
        radius=radius,
        fill=255,
    )
    image.paste(gradient, mask=mask)
    draw = ImageDraw.Draw(image)

    white = (255, 255, 255, 255)
    width = int(canvas * 0.0703)
    points = [
        (int(canvas * 0.3515), int(canvas * 0.287)),
        (int(canvas * 0.3515), int(canvas * 0.451)),
        (int(canvas * 0.412), int(canvas * 0.512)),
        (int(canvas * 0.588), int(canvas * 0.512)),
        (int(canvas * 0.648), int(canvas * 0.572)),
        (int(canvas * 0.648), int(canvas * 0.701)),
    ]
    draw.line(points, fill=white, width=width, joint="curve")
    draw.line(
        [
            (int(canvas * 0.3515), int(canvas * 0.512)),
            (int(canvas * 0.3515), int(canvas * 0.701)),
        ],
        fill=white,
        width=width,
    )
    node_radius = int(canvas * 0.080)
    for x, y in (
        (int(canvas * 0.3515), int(canvas * 0.266)),
        (int(canvas * 0.3515), int(canvas * 0.734)),
        (int(canvas * 0.648), int(canvas * 0.734)),
    ):
        draw.ellipse((x - node_radius, y - node_radius, x + node_radius, y + node_radius), fill=white)

    return image.resize((size, size), Image.Resampling.LANCZOS)


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
