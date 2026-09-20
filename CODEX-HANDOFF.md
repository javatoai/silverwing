# silverwing 2.0.0 交接

## 交接目的

在 Codex Desktop 退出并重新从 `D:\workspace_ai\silverwing` 打开本项目后，继续验证并推进 silverwing 的功能开发。不要丢弃现有工作区改动。

## 当前名称约定

- 产品显示名：`silverwing`
- CLI、配置目录、任务标识与安装产物：`silverwing`
- Kotlin/JVM 包：`com.snowball.silverwing`
- 环境变量：`SILVERWING_*`
- Codex Skill：`$silverwing`

## Git 状态

- 当前分支：`codex/silverwing-command-and-ui`
- GitHub 远程尚未改名；用户确认新的仓库地址后，再显式更新 remote 并推送。
- 当前有大量未提交路径，主要是全仓包路径和产品命名迁移。它们是本次工作成果，不能 `reset`、`checkout` 或丢弃。
- 本次改名工作尚未提交、推送或创建 Tag；除非用户后续明确要求，否则不要执行这些操作。

## 已完成的改造

- 产品统一为 `silverwing`，版本为 `2.0.0`。
- Kotlin/JVM 名称空间已迁移为 `com.snowball.silverwing`；环境变量使用 `SILVERWING_*`。
- Gradle 根项目、桌面窗口与安装产物、CLI、文档、发布脚本、任务元数据与运行时目录均已更新。
- 官网 HTML 与静态截图暂不作为本次收尾范围；静态截图仍保留历史界面品牌，后续单独更新。
- CLI 仅保留 `silverwing`；便携脚本位于 `cli/src/main/portable/bin/`。
- Skill 位于 `skills/silverwing`，frontmatter 名称为 `silverwing`，示例调用 `silverwing` CLI。
- 任务清单文件为 `silverwing.json`，任务运行时标识统一使用 `.silverwing`。
- 本地配置根目录为 `~/silverwing/config/`。
- `ConfigStore` 仍向业务层提供完整 `AppConfig`，但按下列严格校验分片保存，仅写入有变化的分片：

  ```text
  layout.json
  workspace.json
  services.json
  tag.json
  tools.json
  git.json
  integrations.json
  appearance.json
  ```

- 配置导入、导出和备份使用 `silverwing-config-<timestamp>.zip`，包含全部分片，并在替换前完成 ZIP 安全与关联校验。

详细设计与使用说明优先参考：

- `docs/CONFIGURATION.md`
- `docs/ARCHITECTURE.md`
- `docs/VERSIONING.md`
- `docs/RELEASE.md`
- `core/src/main/kotlin/com/snowball/silverwing/core/infrastructure/ConfigStore.kt`
- `core/src/main/kotlin/com/snowball/silverwing/core/infrastructure/Paths.kt`

## 已验证结果

以下命令在本次目录与命名迁移完成后已通过：

```powershell
.\gradlew.bat :core:test :desktop:test :cli:test --no-daemon --no-configuration-cache
.\gradlew.bat :cli:installDist --no-daemon --no-configuration-cache
.\gradlew.bat :desktop:packageDistributionForCurrentOS --no-daemon --no-configuration-cache
& '.\cli\build\install\silverwing\bin\silverwing.bat' --help
git diff --check
```

- 全量测试通过；两个 macOS/POSIX 环境专用测试按设计跳过。
- CLI `--help` 仅展示 `silverwing`。
- Windows 安装包构建成功，产物名为 `silverwing-2.0.0.exe` 和 `silverwing-2.0.0.msi`。
- `git diff --check` 无空白错误；部分已有 CRLF 文件仅提示 Git 将在后续 touch 时转换为 LF。
- 重命名完成后，应扫描源码、文档、安装器、Skill、配置与任务标识，确认没有遗留的旧产品标识。

## 恢复后的建议步骤

1. 先确认物理目录已迁移，且 Git 分支、远程和未提交改动数量均符合上文。
2. 从 `D:\workspace_ai\silverwing` 重新运行 `git diff --check`；如代码被用户或环境改动，再按影响范围复跑测试。
3. 新程序只使用 `~/silverwing/config/`。
4. 等待用户决定是否审查、提交、推送或启动桌面程序。

## Suggested skills

- `ponytail`：任何后续代码修改先采用最小、标准库优先的实现。
- `systematic-debugging`：若目录迁移、构建或配置读取出现异常，先定位根因。
- `handoff`：再次需要跨会话时更新本文件或新建交接文档。
