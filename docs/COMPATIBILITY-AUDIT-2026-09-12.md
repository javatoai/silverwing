# Windows / macOS 兼容性与 Bug 扫描（2026-09-12）

## 基线与范围

项目从 `3e82aaa` 安全快进到 `origin/master` 的 `3b5b7cf`（1.0.4），同步前工作区干净；本次修复版本为 `1.0.5`。本轮审查 Core 工作区生命周期、Git 身份与锁、命令与开发工具探测、Desktop 操作状态、CLI 启动脚本和发布测试配置；第二轮补查 Agent 协议、需求文档、模板存储、Tag 恢复、任务迁移与桌面异步状态。

所有修复保留在本地，未提交、推送或发布。为让 Windows 工作区保存 Unix 执行位，`gradlew` 与 `scripts/build-macos.sh` 的执行权限已记录到 Git 暂存区；其他修改仍需与它们一起审阅。

## 第一轮已确认问题与修复

| 问题 | 触发条件与影响 | 修复与验证方式 |
| --- | --- | --- |
| Git 路径别名误判 | macOS `/var` / `/private/var`、Windows junction 或符号链接让 Git 返回路径与配置路径拼写不同；状态读取、创建、修复、提交或模块移除可能拒绝有效工作区 | 复用 `canonicalOrNormalized()` 比较文件系统真实路径；增加别名状态读取和真实 Git Worktree 创建测试 |
| 同一目录使用不同锁 | 同一仓库或任务通过不同路径别名进入，可能得到不同锁文件 | 对最近存在的父目录解析真实路径后计算锁键，保留统一小写的既有保守策略；验证别名共享锁键及目录创建前后稳定性 |
| macOS 开发工具 PATH 探测遗漏 | Finder 启动的桌面进程未继承登录 shell PATH，Homebrew 等安装位置可能无法发现 | 使用 `/bin/zsh -lc` 执行固定工具名的 `command -v`；用注入命令执行器验证调用与解析 |
| Genbu 自动发现选中不可执行文件 | macOS/Linux 候选目录存在同名普通文件但没有执行位，遮蔽后续可运行的 Genbu | 自动发现同时检查普通文件和执行权限，与手工路径校验一致；增加 POSIX 回归用例 |
| 失败回调异常导致界面持续忙碌 | 主操作失败后，功能控制器的失败回调再次抛异常，公共操作状态无法结束 | 将回调异常附加到原始错误，始终更新失败状态；回归测试验证 busy 清除及两层错误保留 |
| Unix 脚本格式与权限不稳定 | `core.autocrlf=true` 将 shell 脚本检出为 CRLF；原 `gradlew` 与 macOS 构建脚本没有 Git 执行位 | `.gitattributes` 固定 Unix 入口为 LF；补齐 100755 执行位；检查实际字节、Git 属性与 shell 语法 |
| Windows 批处理启动参数被重新解析 | `.cmd` / `.bat` 路径或工作区含空格、`&`、`%` 等字符时，终端和开发工具启动可能失败或收到错误路径 | 统一批处理启动封装，通过子进程环境变量传值并关闭延迟展开；已完成 Java 21 真实隔离夹具校验，覆盖空格、`&`、`%`、单引号、`!`、`^`、PATH 查找及盘符根目录 |

## 第二轮已确认问题与修复

以下源码修复及对应回归测试均已在本地完成。发布工作流只完成本地修改与检查，其远端行为仍需后续 CI 验证。

| 问题 | 触发条件与影响 | 修复与验证方式 |
| --- | --- | --- |
| Agent apply 返回旧资料目录 | plan 后出现同需求的另一处合法资料目录，apply 复用新目录，但成功记录、status 和重复 apply 仍返回旧路径 | 成功记录回填 `materialized.plan`，三种返回路径均有回归断言 |
| 同次操作的模块分支存在父子 ref 冲突 | 同一仓库的最终模块目标同时包含 `feature/foo` 和 `feature/foo/bar`，直到 Git 写入才发现冲突 | 创建任务、添加服务及添加模块共用最终目标校验；验证冲突在副作用前被拒绝、显式覆盖有效、不同仓库可使用同名分支；本检查不扫描全部现有 Git ref |
| Tag 候选碰撞及恢复处理不一致 | 候选 Tag 被其他提交占用，正常构建或 PARTIAL 恢复未一致递增候选，操作无法继续 | 统一本地、远端碰撞的有限重试与恢复路径，保留已占用 ref；真实 Git 用例验证跨分支递增、失败后恢复及原注释 Tag 对象保持不变 |
| 需求总览被覆盖、旧 Sprint 空 ID 无法复用 | 首次补齐 Agent 文档可能覆盖已有需求总览；桌面历史目录仅有 Sprint 名称，后续获得 ID 时被判不匹配 | 保留已有总览，同名 Sprint 保留或补齐已知 ID，仍拒绝两个不同的非空 ID；覆盖文档保留与历史目录复用 |
| Agent 指令冲突提示丢失或再次覆盖磁盘修改 | 多文件冲突只保留一处提示；用户确认期间文件再次变化，旧确认仍可能覆盖最新内容 | 按文件排队保留冲突，确认写入前再次核对磁盘内容，保存后跟踪实际生成内容；覆盖多文件冲突及确认期间再次修改 |
| 模板库并发更新丢失 | 两个操作同时读取模板库后分别写回，后写入者覆盖另一项更新 | 读取、校验、修改、原子写回纳入同一跨实例及跨进程锁；并发添加、编辑和删除测试通过 |
| Meegle 空值解析及显式刷新失效 | JSON `null` 被当作字符串使用；用户显式刷新工具探测时仍沿用旧 PATH 缓存 | 安全读取可选 JSON 成员，保留合法标题；显式刷新使登录 shell PATH 缓存失效，普通调用仍缓存；空值与缓存回归通过 |
| 取消创建后迟到预检继续创建 | 关闭或取消创建流程时预检尚未返回，旧回调仍触发任务创建；预检期间改动输入导致创建参数不一致 | 取消预检 Job 并校验流程代次，预检与确认使用同一不可变提交快照；覆盖取消、过期回调及可变输入列表 |
| 设置探测旧快照覆盖新修改 | 异步工具探测期间用户修改设置，探测完成后写回旧配置快照 | 仅合并探测负责且用户尚未改动的字段；四种探测入口分别验证其他设置与手工路径不被覆盖 |
| 独立 clone 迁移后 owner 仍指向旧路径 | 迁移任务根目录后，独立 clone 的归属记录及仓库路径未同步，后续校验、删除可能失败 | 移动前验证归属，原子更新 owner 和实际仓库路径；夹具分别选择同文件系统移动和跨文件系统复制分支，覆盖迁移后删除、回滚与日志恢复，并拒绝缺失或错误 owner；未实际跨磁盘验收 |
| continuous Release 标签未跟随打包提交 | 持续发布替换了产物，但已有 `continuous` ref 仍指向旧提交 | 发布前创建或更新该 ref 至本次 `GITHUB_SHA`，失败则终止发布，版本标签不进入该步骤；未执行发布 API，待后续 CI 验证 |
| 干净构建漏打包 CLI 版本文件 | 原版本文件生成任务使用 `Copy` 读取文本资源，实际构建显示 `NO-SOURCE`，便携包缺少 `cli/VERSION` | 改为声明版本输入与文件输出的专用 Gradle 任务；基线版本 `1.0.4` 的干净打包及增量构建检查通过，版本升至 `1.0.5` 后会随正式构建重新生成 |

## 测试夹具与 CI

- Genbu 夹具在支持 POSIX 的文件系统上显式设置执行权限；移除两个 macOS 权限夹具的跳过配置。
- 共用 Git 测试辅助命令使用隔离的全局配置，固定测试仓库的换行与签名设置，并显式设置初始分支；未修改用户的全局 Git 配置。
- 路径别名夹具在 Windows 无符号链接权限时使用测试临时目录内的 Junction；真实身份通过 `Files.isSameFile` 验证，本轮三个原先跳过的别名场景均已通过。
- 保留 `skipHostedGitIntegrationTests`：当前没有充分证据证明全部托管 Runner 差异已消除。
- Windows 原生目录选择器的 `IShellItemArray` 调用槽位经 Windows SDK 头文件核对，现有 `GetCount=7`、`GetItemAt=8` 正确，无需修改。接口成员可核对 [Microsoft 文档](https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nn-shobjidl_core-ishellitemarray)。

## 本轮验证

执行环境为 Windows、JDK 21，未传入任何发布测试排除参数。最终结果汇总：

| 验证 | 结果 |
| --- | --- |
| Core 全量测试及最终别名夹具补测 | 470 项：468 通过、2 跳过、0 失败、0 错误 |
| Desktop 全量测试 | 158 项全部通过 |
| 合计 | 628 项：626 通过、2 跳过 |
| Desktop 编译及 `createDistributable` | 成功生成 Windows 应用目录，包含 CLI、7 个依赖 JAR 及自带 Java runtime |
| Windows 批处理真实进程 | 中文、空格、括号、`&`、`%`、单引号、`!`、`^`、PATH 查找、盘符根路径及 `--new-window` 参数测试通过 |
| 便携 CLI 脱离系统 JDK | 复制至含中文及空格的路径，将进程内 `JAVA_HOME` 指向不存在目录、PATH 限制为 Windows 系统目录后，`silverwing.cmd --help` 和 `silverwing.cmd agent inspect --json` 均成功 |
| CLI 数据隔离 | inspect 使用工作区 `build/compatibility-audit/isolated-home` 作为 `user.home`；输出指向该临时目录，并正确提示未配置需求资料目录 |
| CLI 版本文件 | `1.0.5` 产物字节为 `31-2E-30-2E-35-0A`，即 `1.0.5` 加 LF |
| 版本生成任务缓存 | 重复运行 `:desktop:writePortableCliVersion` 成功复用 Gradle configuration cache，输出保持有效 |
| Unix 入口与差异检查 | 三个入口均为 LF 且通过 `bash -n`，执行位为 100755；暂存区及工作区的 `git diff --check` 均通过 |
| 发布工作流静态检查 | YAML 解析及新增 continuous 标签步骤的 `bash -n` 均通过；未执行其中的命令或 API |

两项跳过分别为 POSIX 不可执行 Genbu 候选过滤、macOS 登录 shell 中 Node shebang CLI 的运行环境测试，均不能在当前 Windows 环境直接证明。

主要构建命令（均附加 `--console=plain --max-workers=4 -I .gradle/compatibility-audit.init.gradle`）：

```powershell
.\gradlew.bat clean :core:test :desktop:compileKotlin :desktop:createDistributable
.\gradlew.bat :desktop:clean :desktop:test :desktop:createDistributable
.\gradlew.bat :core:test --tests com.snowball.silverwing.core.FileLockingTest --tests com.snowball.silverwing.core.WorkspaceGitStatusTest --tests com.snowball.silverwing.core.WorkspacePathAliasIntegrationTest :desktop:test :desktop:createDistributable
```

临时 init script 只将测试进程并发设为 4，位于已忽略的 `.gradle` 目录，不改变项目默认构建设置。第二条命令首次运行有一条新测试对协程异常文本格式的断言过严；调整为验证原始错误、附加错误和 busy 清除后，第三条命令重新执行全部 Desktop 测试并通过。

最终 Core 统计基于完整测试 XML 快照，叠加三个类的最终补测结果；快照及便携 CLI 输出保存在已忽略的 `build/compatibility-audit/`。Windows 应用目录为 `desktop/build/compose/binaries/main/app/silverwing/`。

## 验证边界与已有支持限制

- 当前执行环境为 Windows / JDK 21；macOS 专属运行、POSIX 权限与 DMG 安装尚未在 Mac 实机或本轮 CI 验证。
- 本轮未执行桌面 GUI 人工验收，也未安装 EXE/MSI；便携 CLI 验证不等于所有图形界面及安装流程已经验收。
- macOS 签名公证、Terminal Automation 权限、Codex 协议可用性预检仍见 [已知问题](KNOWN-ISSUES.md)。
- 沿用现有支持范围：Apple Silicon Mac、Windows x64；macOS CLI 一键安装及 Cursor 独立启动器的 Finder 自动发现范围保持既有约定。
- 锁键解析规则更新后，旧进程仍可能持有旧规则生成的锁；使用新构建前应结束旧版本 silverwing/CLI 操作。
