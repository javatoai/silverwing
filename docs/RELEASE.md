# 发布指南

版本升级规则见 [VERSIONING.md](VERSIONING.md)。发布前必须确认本次是否影响任一配置分片或 `silverwing.json` 字段。

## 分支与标签

- `master`：默认分支，持续集成和 `continuous` 预发布的来源；
- `vX.Y.Z`：正式版本标签，例如 `v1.0.0`；
- `codex/feature-*`：功能开发分支，合并前完成测试与审查。

正式发布应在 `master` 已通过本地验证后创建 annotated tag。不要移动既有版本标签；需要修复时发布新的补丁版本。

```powershell
git switch master
git pull --ff-only origin master
git tag -a vX.Y.Z -m "Release X.Y.Z"
git push origin master vX.Y.Z
git push github master vX.Y.Z
```

## 本地打包

Windows 可构建绿色目录、portable ZIP、Setup EXE 和 MSI：

```powershell
.\scripts\build-windows.ps1
```

构建产物位于：

```text
desktop/build/compose/binaries/main/app/
desktop/build/compose/binaries/main/zip/
desktop/build/compose/binaries/main/exe/
desktop/build/compose/binaries/main/msi/
```

macOS 必须在 macOS 主机上构建 DMG：

```bash
./scripts/build-macos.sh
```

DMG 产物位于 `desktop/build/compose/binaries/main/dmg/`。不要在 Windows 上声称已验证 macOS DMG。

## GitHub Release 工作流

`Release packages` 工作流在推送 `v*` 标签时运行，并创建对应正式 GitHub Release。

工作流先在 Windows 与 macOS 运行测试和桌面编译，再分别构建 Windows portable ZIP、EXE、MSI 与 macOS DMG，最后上传并发布 GitHub Release。

### 飞书知识库同步

GitHub Release 发布成功后，工作流会在指定知识库根页面下创建对应 Tag 的子文档，写入 GitHub 自动 Release Notes，并将全部 Release 附件以文件卡片形式附在文档末尾。

首次配置需要在 GitHub 仓库的 `Settings → Secrets and variables → Actions → Repository secrets` 中设置：

- `FEISHU_RELEASE_APP_ID`：飞书企业自建应用 App ID；
- `FEISHU_RELEASE_APP_SECRET`：该应用的 App Secret；
- `FEISHU_RELEASE_PARENT_NODE_TOKEN`：目标知识库根页面的 Wiki node token。

应用还必须拥有知识库读取、在目标节点下创建/编辑 Docx、以及上传文档附件的权限，并被授予目标根页面的编辑权限。同步使用固定版本的官方 Lark CLI，可自动分片上传大于 20 MB 的安装包。

同一 Tag 重跑时会复用同名子文档，完整刷新自动生成的正文和附件卡片；知识库中出现多个同名 Tag 子文档时，工作流会失败而不会猜测覆盖目标。飞书同步失败会使 GitHub Actions 失败，但不会撤销已经发布的 GitHub Release。

## 发布检查清单

1. `build.gradle.kts`、桌面安装包版本和变更记录一致；
2. `gradlew test :desktop:compileKotlin` 通过；
3. Windows 打包并启动 portable ZIP；
4. 在 macOS runner 或设备上验证 DMG；
5. 确认工作区不含未跟踪的用户配置、日志或凭据；
6. 将 `master` 与新 `vX.Y.Z` 标签推送到 GitLab `origin` 和 GitHub `github`；
7. 下载 Release 附件并完成一次启动冒烟测试；
8. 确认 Actions Summary 中的飞书发布页与附件清单。
