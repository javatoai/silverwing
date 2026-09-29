#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import publish_feishu_release as subject  # noqa: E402


class FakeLark:
    def __init__(self, items: list[dict[str, Any]]) -> None:
        self.items = items
        self.calls: list[tuple[list[str], str | None]] = []

    def __call__(self, command: list[str], stdin: str | None = None) -> str:
        self.calls.append((command, stdin))
        if command[:3] == ["lark-cli", "config", "init"]:
            return "configured"
        if command[:3] == ["lark-cli", "wiki", "+node-get"]:
            return json.dumps({"ok": True, "data": {"node": {"space_id": "space-1"}}})
        if command[:3] == ["lark-cli", "wiki", "+node-list"]:
            return json.dumps({"ok": True, "data": {"items": self.items}})
        if command[:3] == ["lark-cli", "docs", "+create"]:
            return json.dumps(
                {
                    "ok": True,
                    "data": {
                        "document": {
                            "document_id": "doc-created",
                            "url": "https://example.feishu.cn/docx/doc-created",
                        }
                    },
                }
            )
        if command[:3] in (
            ["lark-cli", "docs", "+update"],
            ["lark-cli", "docs", "+media-insert"],
        ):
            return json.dumps({"ok": True, "data": {"document": {}}})
        raise AssertionError(f"Unexpected Lark CLI command: {command}")


class PublishFeishuReleaseTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.assets_directory = self.root / "assets"
        self.assets_directory.mkdir()
        for name in ("silverwing-windows.zip", "silverwing-macos.dmg"):
            (self.assets_directory / name).write_bytes(b"artifact")
        self.release = {
            "tagName": "v9.9.9",
            "url": "https://github.com/javatoai/silverwing/releases/tag/v9.9.9",
            "publishedAt": "2026-09-29T08:00:00Z",
            "body": "- Fix release synchronization",
            "assets": [
                {"name": "silverwing-windows.zip"},
                {"name": "silverwing-macos.dmg"},
            ],
        }
        self.environment = {
            "FEISHU_RELEASE_APP_ID": "cli_example",
            "FEISHU_RELEASE_APP_SECRET": "app-secret",
            "FEISHU_RELEASE_PARENT_NODE_TOKEN": "wiki-root",
        }

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_new_tag_creates_page_and_attaches_every_release_asset(self) -> None:
        fake = FakeLark([])

        document_url, created, attachment_count = subject.synchronize_release(
            self.release,
            self.assets_directory,
            self.environment,
            fake,
            self.root,
        )

        self.assertTrue(created)
        self.assertEqual("https://example.feishu.cn/docx/doc-created", document_url)
        self.assertEqual(2, attachment_count)
        create_call = next(command for command, _ in fake.calls if command[2] == "+create")
        self.assertEqual("v9.9.9", create_call[create_call.index("--title") + 1])
        self.assertEqual("wiki-root", create_call[create_call.index("--parent-token") + 1])
        attachments = [command for command, _ in fake.calls if command[2] == "+media-insert"]
        self.assertEqual(2, len(attachments))
        self.assertEqual({"silverwing-windows.zip", "silverwing-macos.dmg"}, {Path(call[call.index("--file") + 1]).name for call in attachments})
        self.assertFalse(list(self.root.glob(".feishu-release-*.md")))

    def test_existing_tag_page_is_overwritten_then_reattached(self) -> None:
        fake = FakeLark(
            [
                {
                    "title": "v9.9.9",
                    "node_type": "origin",
                    "obj_type": "docx",
                    "obj_token": "doc-existing",
                    "url": "https://example.feishu.cn/wiki/wiki-existing",
                }
            ]
        )

        document_url, created, attachment_count = subject.synchronize_release(
            self.release,
            self.assets_directory,
            self.environment,
            fake,
            self.root,
        )

        self.assertFalse(created)
        self.assertEqual("https://example.feishu.cn/wiki/wiki-existing", document_url)
        self.assertEqual(2, attachment_count)
        self.assertFalse(any(command[2] == "+create" for command, _ in fake.calls))
        update_call = next(command for command, _ in fake.calls if command[2] == "+update")
        self.assertEqual("doc-existing", update_call[update_call.index("--doc") + 1])
        self.assertEqual("overwrite", update_call[update_call.index("--command") + 1])

    def test_duplicate_tag_pages_fail_before_writing(self) -> None:
        duplicate = {
            "title": "v9.9.9",
            "node_type": "origin",
            "obj_type": "docx",
            "obj_token": "doc-duplicate",
        }
        fake = FakeLark([duplicate, {**duplicate, "obj_token": "doc-other"}])

        with self.assertRaisesRegex(subject.PublishError, "多个同名 Tag 页面"):
            subject.synchronize_release(
                self.release,
                self.assets_directory,
                self.environment,
                fake,
                self.root,
            )

        self.assertFalse(any(command[1:3] == ["docs", "+create"] for command, _ in fake.calls))
        self.assertFalse(any(command[1:3] == ["docs", "+update"] for command, _ in fake.calls))

    def test_markdown_keeps_release_notes_and_attachment_list(self) -> None:
        markdown = subject.release_markdown(
            self.release,
            ["silverwing-windows.zip", "silverwing-macos.dmg"],
        )

        self.assertIn("## 更新内容", markdown)
        self.assertIn("Fix release synchronization", markdown)
        self.assertIn("`silverwing-windows.zip`", markdown)
        self.assertIn("`silverwing-macos.dmg`", markdown)


if __name__ == "__main__":
    unittest.main()
