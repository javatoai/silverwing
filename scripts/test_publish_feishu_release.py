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
    def __init__(self, fail_media_insert: bool = False) -> None:
        self.calls: list[tuple[list[str], str | None]] = []
        self.fail_media_insert = fail_media_insert

    def __call__(self, command: list[str], stdin: str | None = None) -> str:
        self.calls.append((command, stdin))
        if command[:3] == ["lark-cli", "config", "init"]:
            return "configured"
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
            if command[:3] == ["lark-cli", "docs", "+media-insert"] and self.fail_media_insert:
                raise subject.PublishError("attachment upload failed")
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
        fake = FakeLark()

        document_id, document_url, created, attachment_count = subject.synchronize_release(
            self.release,
            self.assets_directory,
            self.environment,
            fake,
            self.root,
        )

        self.assertEqual("doc-created", document_id)
        self.assertTrue(created)
        self.assertEqual("https://example.feishu.cn/docx/doc-created", document_url)
        self.assertEqual(2, attachment_count)
        create_call = next(command for command, _ in fake.calls if command[2] == "+create")
        self.assertEqual("v9.9.9", create_call[create_call.index("--title") + 1])
        self.assertEqual("wiki-root", create_call[create_call.index("--parent-token") + 1])
        attachments = [command for command, _ in fake.calls if command[2] == "+media-insert"]
        self.assertEqual(2, len(attachments))
        self.assertEqual({"silverwing-windows.zip", "silverwing-macos.dmg"}, {Path(call[call.index("--file") + 1]).name for call in attachments})
        self.assertFalse(any(command[1:3] == ["wiki", "+node-list"] for command, _ in fake.calls))
        self.assertNotIn("--format", create_call)
        self.assertFalse(list(self.root.glob(".feishu-release-*.md")))

    def test_existing_release_marker_is_overwritten_then_reattached(self) -> None:
        fake = FakeLark()
        self.release["body"] += "\n<!-- silverwing-feishu-document: doc-existing -->\n"

        document_id, document_url, created, attachment_count = subject.synchronize_release(
            self.release,
            self.assets_directory,
            self.environment,
            fake,
            self.root,
        )

        self.assertFalse(created)
        self.assertEqual("doc-existing", document_id)
        self.assertIsNone(document_url)
        self.assertEqual(2, attachment_count)
        self.assertFalse(any(command[2] == "+create" for command, _ in fake.calls))
        update_call = next(command for command, _ in fake.calls if command[2] == "+update")
        self.assertEqual("doc-existing", update_call[update_call.index("--doc") + 1])
        self.assertEqual("overwrite", update_call[update_call.index("--command") + 1])
        self.assertNotIn("--format", update_call)

    def test_duplicate_release_markers_fail_before_writing(self) -> None:
        fake = FakeLark()
        self.release["body"] += (
            "\n<!-- silverwing-feishu-document: doc-one -->"
            "\n<!-- silverwing-feishu-document: doc-two -->\n"
        )

        with self.assertRaisesRegex(subject.PublishError, "多个飞书页面标记"):
            subject.synchronize_release(
                self.release,
                self.assets_directory,
                self.environment,
                fake,
                self.root,
            )

        self.assertFalse(any(command[1:3] == ["docs", "+create"] for command, _ in fake.calls))
        self.assertFalse(any(command[1:3] == ["docs", "+update"] for command, _ in fake.calls))

    def test_attachment_failure_keeps_result_for_a_safe_retry(self) -> None:
        fake = FakeLark(fail_media_insert=True)
        result_path = self.root / "result.json"

        with self.assertRaisesRegex(subject.PublishError, "attachment upload failed"):
            subject.synchronize_release(
                self.release,
                self.assets_directory,
                self.environment,
                fake,
                self.root,
                result_path,
            )

        result = json.loads(result_path.read_text(encoding="utf-8"))
        self.assertEqual("doc-created", result["documentId"])
        self.assertTrue(result["created"])
        self.assertEqual(0, result["attachmentCount"])
        self.assertFalse(result["attachmentsCompleted"])

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
