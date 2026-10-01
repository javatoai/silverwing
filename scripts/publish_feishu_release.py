#!/usr/bin/env python3
"""Sync a published GitHub Release into a Feishu Wiki release page."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path
from typing import Any


class PublishError(RuntimeError):
    """A release could not be synchronized safely."""


CommandInvoker = Callable[[list[str], str | None], str]


RELEASE_DOCUMENT_MARKER = re.compile(
    r"<!--\s*silverwing-feishu-document:\s*([A-Za-z0-9_-]+)\s*-->"
)


def require_environment(environment: Mapping[str, str], name: str) -> str:
    value = environment.get(name, "").strip()
    if not value:
        raise PublishError(f"缺少 GitHub Secret：{name}")
    return value


def redact(text: str, sensitive_values: Sequence[str]) -> str:
    result = text
    for value in sensitive_values:
        if value:
            result = result.replace(value, "***")
    result = re.sub(r"\b(?:t|u)-[A-Za-z0-9_-]+\b", "***", result)
    return result.strip()


def command_invoker(
    working_directory: Path,
    sensitive_values: Sequence[str],
) -> CommandInvoker:
    def invoke(command: list[str], stdin: str | None = None) -> str:
        try:
            completed = subprocess.run(
                command,
                cwd=working_directory,
                input=stdin,
                text=True,
                encoding="utf-8",
                errors="replace",
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=False,
            )
        except OSError as error:
            raise PublishError(f"无法执行 {command[0]}：{error}") from error
        if completed.returncode != 0:
            diagnostic = redact(
                "\n".join(part for part in (completed.stderr, completed.stdout) if part),
                sensitive_values,
            )
            raise PublishError(
                f"{' '.join(command[:3])} 执行失败（退出码 {completed.returncode}）"
                + (f"：{diagnostic}" if diagnostic else "")
            )
        return completed.stdout

    return invoke


def lark_data(invoke: CommandInvoker, arguments: list[str]) -> dict[str, Any]:
    output = invoke(["lark-cli", *arguments], None)
    try:
        payload = json.loads(output)
    except json.JSONDecodeError as error:
        raise PublishError(f"Lark CLI 返回了无法解析的 JSON：{output.strip()}") from error
    if payload.get("ok") is False:
        error = payload.get("error") or {}
        message = error.get("message") or error.get("hint") or "未知错误"
        raise PublishError(f"Lark CLI 操作失败：{message}")
    data = payload.get("data")
    if not isinstance(data, dict):
        raise PublishError("Lark CLI 返回中缺少 data")
    return data


def load_release(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise PublishError(f"无法读取 GitHub Release 元数据：{error}") from error
    if not isinstance(payload, dict):
        raise PublishError("GitHub Release 元数据格式错误")
    for key in ("tagName", "url", "assets"):
        if key not in payload:
            raise PublishError(f"GitHub Release 元数据缺少 {key}")
    tag = payload["tagName"]
    if not isinstance(tag, str) or not tag or "\n" in tag or "\r" in tag:
        raise PublishError("GitHub Release Tag 不合法")
    if not isinstance(payload["url"], str) or not payload["url"]:
        raise PublishError("GitHub Release 链接不合法")
    if not isinstance(payload["assets"], list) or not payload["assets"]:
        raise PublishError("GitHub Release 不包含可同步的构建产物")
    return payload


def release_assets(release: Mapping[str, Any], assets_directory: Path) -> list[tuple[str, Path]]:
    if not assets_directory.is_dir():
        raise PublishError(f"构建产物目录不存在：{assets_directory}")
    root = assets_directory.resolve()
    names: list[str] = []
    for asset in release["assets"]:
        if not isinstance(asset, dict):
            raise PublishError("GitHub Release 产物格式错误")
        name = asset.get("name")
        if not isinstance(name, str) or not name or Path(name).name != name:
            raise PublishError("GitHub Release 包含不安全的产物文件名")
        if name in names:
            raise PublishError(f"GitHub Release 包含重复产物：{name}")
        names.append(name)

    files: list[tuple[str, Path]] = []
    for name in names:
        candidate = (root / name).resolve()
        if root not in candidate.parents or not candidate.is_file():
            raise PublishError(f"未下载到 GitHub Release 产物：{name}")
        files.append((name, candidate))
    downloaded = {path.name for path in root.iterdir() if path.is_file()}
    if downloaded != set(names):
        raise PublishError("下载目录与 GitHub Release 产物清单不一致")
    return files


def release_markdown(release: Mapping[str, Any], asset_names: Sequence[str]) -> str:
    tag = release["tagName"].replace("\\", "\\\\").replace("`", "\\`")
    release_url = release["url"]
    published_at = release.get("publishedAt")
    body = release.get("body")
    release_notes = body.strip() if isinstance(body, str) and body.strip() else "_本次 GitHub Release 未提供更新说明。_"
    release_notes = release_notes.replace("<", "\\<")

    lines = [
        "## 发布信息",
        "",
        f"- Tag：`{tag}`",
        f"- GitHub Release：[查看发布页]({release_url})",
    ]
    if isinstance(published_at, str) and published_at:
        lines.append(f"- 发布时间：{published_at}")
    lines += [
        "",
        "## 更新内容",
        "",
        release_notes,
        "",
        "## 构建产物",
        "",
        "以下构建产物已作为文件附件附在本文档末尾：",
        "",
    ]
    lines.extend(f"- `{name.replace('`', '\\`')}`" for name in asset_names)
    return "\n".join(lines) + "\n"


def release_document_id(release: Mapping[str, Any]) -> str | None:
    body = release.get("body")
    if body is None:
        return None
    if not isinstance(body, str):
        raise PublishError("GitHub Release 更新说明格式错误")
    matches = RELEASE_DOCUMENT_MARKER.findall(body)
    if len(matches) > 1:
        raise PublishError("GitHub Release 中存在多个飞书页面标记")
    return matches[0] if matches else None


def write_markdown_file(working_directory: Path, content: str) -> Path:
    with tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        suffix=".md",
        prefix=".feishu-release-",
        dir=working_directory,
        delete=False,
    ) as temporary:
        temporary.write(content)
        return Path(temporary.name)


def create_or_update_release_page(
    invoke: CommandInvoker,
    parent_node_token: str,
    tag: str,
    markdown_file: Path,
    existing_document_id: str | None,
) -> tuple[str, str | None, bool]:
    content_reference = f"@./{markdown_file.name}"
    if existing_document_id is None:
        data = lark_data(
            invoke,
            [
                "docs",
                "+create",
                "--as",
                "bot",
                "--parent-token",
                parent_node_token,
                "--title",
                tag,
                "--doc-format",
                "markdown",
                "--content",
                content_reference,
            ],
        )
        document = data.get("document")
        if not isinstance(document, dict):
            raise PublishError("飞书未返回新建发布页面")
        document_id = document.get("document_id")
        if not isinstance(document_id, str) or not document_id:
            raise PublishError("飞书未返回新建发布页面 ID")
        url = document.get("url") if isinstance(document.get("url"), str) else None
        return document_id, url, True

    lark_data(
        invoke,
        [
            "docs",
            "+update",
            "--as",
            "bot",
            "--doc",
            existing_document_id,
            "--command",
            "overwrite",
            "--doc-format",
            "markdown",
            "--content",
            content_reference,
        ],
    )
    return existing_document_id, None, False


def append_attachments(
    invoke: CommandInvoker,
    document_id: str,
    assets: Sequence[tuple[str, Path]],
) -> None:
    for _, path in assets:
        lark_data(
            invoke,
            [
                "docs",
                "+media-insert",
                "--as",
                "bot",
                "--doc",
                document_id,
                "--file",
                str(path),
                "--type",
                "file",
                "--file-view",
                "card",
                "--format",
                "json",
            ],
        )


def synchronize_release(
    release: Mapping[str, Any],
    assets_directory: Path,
    environment: Mapping[str, str],
    invoke: CommandInvoker,
    working_directory: Path,
    result_path: Path | None = None,
) -> tuple[str, str | None, bool, int]:
    app_id = require_environment(environment, "FEISHU_RELEASE_APP_ID")
    app_secret = require_environment(environment, "FEISHU_RELEASE_APP_SECRET")
    parent_node_token = require_environment(environment, "FEISHU_RELEASE_PARENT_NODE_TOKEN")
    existing_document_id = release_document_id(release)
    assets = release_assets(release, assets_directory)
    markdown_file = write_markdown_file(
        working_directory,
        release_markdown(release, [name for name, _ in assets]),
    )
    try:
        invoke(
            [
                "lark-cli",
                "config",
                "init",
                "--app-id",
                app_id,
                "--app-secret-stdin",
                "--brand",
                "feishu",
            ],
            app_secret,
        )
        document_id, document_url, created = create_or_update_release_page(
            invoke,
            parent_node_token,
            str(release["tagName"]),
            markdown_file,
            existing_document_id,
        )
        if result_path:
            write_result(
                result_path,
                str(release["tagName"]),
                document_id,
                document_url,
                created,
                attachment_count=0,
                attachments_completed=False,
            )
        append_attachments(invoke, document_id, assets)
        if result_path:
            write_result(
                result_path,
                str(release["tagName"]),
                document_id,
                document_url,
                created,
                attachment_count=len(assets),
                attachments_completed=True,
            )
        return document_id, document_url, created, len(assets)
    finally:
        markdown_file.unlink(missing_ok=True)


def write_summary(tag: str, document_url: str | None, created: bool, attachment_count: int) -> None:
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if not summary_path:
        return
    status = "已创建" if created else "已更新"
    lines = [
        "## 飞书知识库同步",
        "",
        f"- Tag：`{tag}`",
        f"- 页面：{status}",
        f"- 附件：{attachment_count} 个",
    ]
    if document_url:
        lines.append(f"- 文档：[打开飞书发布页]({document_url})")
    with Path(summary_path).open("a", encoding="utf-8") as summary:
        summary.write("\n".join(lines) + "\n")


def write_result(
    path: Path,
    tag: str,
    document_id: str,
    document_url: str | None,
    created: bool,
    attachment_count: int,
    attachments_completed: bool,
) -> None:
    result = {
        "tag": tag,
        "documentId": document_id,
        "documentUrl": document_url,
        "created": created,
        "attachmentCount": attachment_count,
        "attachmentsCompleted": attachments_completed,
    }
    path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-json", type=Path, required=True)
    parser.add_argument("--assets-dir", type=Path, required=True)
    parser.add_argument("--result-json", type=Path)
    arguments = parser.parse_args(argv)
    working_directory = Path.cwd()

    try:
        release = load_release(arguments.release_json)
        app_secret = require_environment(os.environ, "FEISHU_RELEASE_APP_SECRET")
        parent_node_token = require_environment(os.environ, "FEISHU_RELEASE_PARENT_NODE_TOKEN")
        invoke = command_invoker(working_directory, (app_secret, parent_node_token))
        _, document_url, created, attachment_count = synchronize_release(
            release,
            arguments.assets_dir,
            os.environ,
            invoke,
            working_directory,
            arguments.result_json,
        )
        write_summary(str(release["tagName"]), document_url, created, attachment_count)
        print(f"飞书知识库同步完成：{release['tagName']}，附件 {attachment_count} 个")
        return 0
    except PublishError as error:
        print(f"::error title=飞书知识库同步失败::{error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
