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

工作流先在 Windows 与 macOS 校验版本并编译桌面，再分别构建 Windows portable ZIP、EXE、MSI 与 macOS DMG，最后上传并发布 GitHub Release。远程发布使用 `SILVERWING_RELEASE_SKIP_TESTS=true`，不在打包任务中重复执行测试；本地打包脚本默认仍执行完整测试，发布前应完成本地验证。

### 飞书更新文档

发布工作流只负责 GitHub Release，不自动同步飞书 Wiki。发布后由维护者手动将安装包和更新说明上传到[飞书更新文档](https://snowballtech.feishu.cn/wiki/GtDBw8F7Ti8rbLksnF7csNFgn4c)。用户可通过设置“系统与诊断 → 更新”打开该文档并下载新版。

## 发布检查清单

1. `build.gradle.kts`、桌面安装包版本和变更记录一致；
2. `gradlew test :desktop:compileKotlin` 通过；
3. Windows 打包并启动 portable ZIP；
4. 在 macOS runner 或设备上验证 DMG；
5. 确认工作区不含未跟踪的用户配置、日志或凭据；
6. 将 `master` 与新 `vX.Y.Z` 标签推送到 GitLab `origin` 和 GitHub `github`；
7. 下载 Release 附件并完成一次启动冒烟测试；
8. 按需手动上传安装包和更新说明到飞书更新文档。
