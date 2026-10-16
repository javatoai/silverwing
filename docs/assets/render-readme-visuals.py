"""Export the README's brand and banner assets from their HTML/CSS source.

Run from any directory: python docs/assets/render-readme-visuals.py
Requires Python Playwright with Chromium. Use Windows with Segoe UI Semibold
installed to reproduce the wordmark. No external network or app launch is needed.
The original application screenshots and icon are only read, never rewritten.
"""

from pathlib import Path
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread

from playwright.sync_api import sync_playwright


ASSETS = Path(__file__).resolve().parent
SIZES = {"brand": (1280, 280), "banner": (2560, 1440)}


class AssetHandler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(ASSETS.parents[1]), **kwargs)

    def log_message(self, *args):
        pass


def main():
    # Same-origin HTTP also permits the icon's CSS mask, which file: blocks.
    server = ThreadingHTTPServer(("127.0.0.1", 0), AssetHandler)
    Thread(target=server.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{server.server_port}"
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            page = browser.new_page(device_scale_factor=1)
            page.route(
                "**/*",
                lambda route: route.continue_()
                if route.request.url.startswith(base + "/")
                else route.abort(),
            )
            for theme in ("light", "dark"):
                for asset, (width, height) in SIZES.items():
                    page.set_viewport_size({"width": width, "height": height})
                    source = f"{base}/docs/assets/readme-visuals.html"
                    page.goto(f"{source}?theme={theme}&asset={asset}")
                    page.evaluate("document.fonts.ready")
                    page.locator("img").evaluate_all(
                        "images => Promise.all(images.map(image => image.decode()))"
                    )
                    output = ASSETS / f"readme-{asset}-{theme}.png"
                    page.locator(f".{asset}").screenshot(
                        path=str(output), omit_background=True
                    )
                    print(f"{output.name}: {width} x {height}")
            browser.close()
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
