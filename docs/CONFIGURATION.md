# 配置与使用

## 配置目录

Windows 使用 `%USERPROFILE%\silverwing`，macOS 使用 `~/silverwing`。首次启动时，silverwing 会创建应用目录、`tasks` 子目录，以及 `config` 目录下的全部配置分片。默认任务目录无法创建时，程序仍会写入可继续编辑的默认配置、显示错误，并允许在设置中选择其他可写目录。已有配置中的自定义 `taskRoot` 保持不变；程序不会探测、读取、迁移或改写旧产品目录。

说明文件固定保存在：

```text
agents/global/AGENTS.md
agents/groups/<groupId>/AGENTS.md
agents/task-templates.json
```

磁盘文件是唯一可信来源；说明正文不重复保存在配置分片中。

## 分功能严格配置分片

配置不再使用单一 `config.json`。`~/silverwing/config/` 必须恰好包含以下八个 JSON 文件；每个文件均有独立的严格 `"schema": 6` 字段，未知字段、缺失文件、重复/额外 ZIP 条目和不匹配 schema 都会被拒绝，不会被自动修复或迁移。旧 schema 5 配置不会被读取或改写。

| 文件 | 保存内容 |
| --- | --- |
| `layout.json` | 组顺序、名称与组级 Tag 开关 |
| `workspace.json` | 任务根目录、仓库与全局默认分支前缀 |
| `services.json` | 组内服务、模块、Bootstrap 与模块专属快捷命令 |
| `tag.json` | 全局 Tag 开关、任务测试目标分支修改开关与历史保留组数 |
| `tools.json` | 终端、开发工具、全局默认打开工具与临时选择开关 |
| `git.json` | Git 可执行程序与受保护分支 |
| `integrations.json` | Meegle、Lark、Genbu、任务资料目录、AI 需求命名开关、Codex 插件市场和外部 Skill 来源定义 |
| `appearance.json` | 主题与界面显示偏好 |

业务层仍读取和保存完整 `AppConfig`；保存时只原子替换实际发生变化的分片。每次写入前会将完整分片集归档为 `~/silverwing/backups/silverwing-config-<timestamp>.zip`，最多保留最近 10 份。导出、导入和恢复也统一使用包含全部八个分片的 `silverwing-config-<timestamp>.zip`。

Codex 插件市场的本机注册信息、最近成功的插件目录缓存、外部 Skill 的 Git 检出、接管记录和可恢复备份保存在 `~/silverwing/codex/`。它们不属于可导出的配置：导入只恢复来源定义，不会自动联网、注册市场或安装插件与 Skill。

## Tag设置

`tagEnabled` 是全局 Tag 开关，默认值为 `true`。关闭后，桌面端会隐藏 Tag 导航、工作区构建入口、组和模块中的 Tag 配置，以及 Tag Skill 入口；Tag 设置本身仍保留，方便重新开启。核心用例和 `silverwing tag` CLI 也会拒绝新的 Tag 操作。

`allowTaskTagTargetEditing` 控制是否允许在任务详情修改测试目标分支，默认值为 `false`，已有配置未填写时也默认关闭。在“设置 → Git → Tag 设置”中开启后，采用合并模式的服务卡片会显示“更多 → 设置测试目标分支…”；关闭后隐藏该入口和对应的“更多”按钮，已保存的测试目标分支继续用于构建。

`tagHistoryMaxGroups` 控制本地 Tag 构建历史最多保留的组数，默认值为 `3`，允许范围为 `1`–`1000`。单次批量构建产生的记录属于同一组；超过上限时，silverwing 按组的最近更新时间从旧到新清理整组记录，只删除任务目录中的 Tag 操作记录和历史汇总，不删除 Git Tag、代码或任务。应用启动读取历史、任务列表变化、Tag 构建完成或保存该设置后都会执行清理。

## 任务根目录迁移

在设置页选择新的任务根目录后，silverwing 先进行只读预检。旧目录没有任务时直接保存；存在任务时展示迁移方式、任务数、工作区数和数据量，用户确认后才开始迁移。

- 新旧目录不能相同或互相包含；目标必须为空且可写，并有足够空间。
- 所有任务清单必须可读且为当前版本，工作区必须位于对应任务目录内；发现冲突会阻止整批迁移。
- 同磁盘整体移动任务目录；跨磁盘不跟随符号链接地复制并保留基础文件属性。
- 标准 Worktree 执行 `git worktree repair`；独立克隆保留完整 `.git`。
- silverwing 校验 HEAD、分支、仓库身份以及暂存、未暂存和未跟踪文件状态，更新清单路径并重新生成 `AGENTS.md` 系统区。
- 全部任务成功后才原子更新 `taskRoot` 并清理旧目录。清理失败时新目录仍生效，界面会列出待清理路径；迁移日志位于 `~/silverwing/migrations/task-root.json`，下次启动会继续清理或回滚。

任务资料目录由 `requirementMaterialsRoot` 独立管理，不随任务根目录迁移。

工作区策略属于模块而不是服务。同一服务的 `modules` 可以同时包含 Worktree 与独立克隆模块：

`genbuProbeEnabled` 是服务级开关，默认 `false`。开启后，Tag 构建页面会用 `where.exe genbu.exe` 自动发现本机 `genbu` CLI，并在页面打开期间以 `genbu query-tag --json <服务> <精确 Tag>` 查询所有带 Tag 记录的构建、UAT 发版和生产发版三个阶段的状态（初始/构建中/成功/失败）及其返回的完成时间；`genbuServiceName` 默认使用服务展示名称，可在服务配置中改为 Genbu 的实际服务名。自动轮询会跳过已完成 UAT 发布、构建失败、被更晚 Tag 覆盖或已确认未在 Genbu 找到的记录；页面的“刷新 Genbu”会强制重新查询全部带 Tag 记录。当某条记录的 Genbu 构建结果为失败时，行内提供“重新打Tag”按钮：重新执行完整 Tag 流程，版本号在失败 Tag 基础上自动 +1，并原地替换该条构建记录。

```json
{
"masterBranch": "origin/master",
"testTagBaselineRef": "origin/release/test",
"modules": [
  {
    "id": "feign-master",
    "name": "主线客户端",
    "strategy": "STANDARD_WORKTREE",
    "masterBranch": null,
    "tagEnabled": true,
    "tagMode": "MERGE_TO_TARGET_BRANCH",
    "tagTargetRef": null,
    "tagMessagePrefix": "Tag"
  },
  {
    "id": "feign-development",
    "name": "开发客户端",
    "strategy": "INDEPENDENT_CLONE",
    "masterBranch": "origin/development",
    "tagEnabled": false,
    "tagMode": "CURRENT_BRANCH",
    "tagTargetRef": null,
    "tagMessagePrefix": "Tag"
  }
]
}
```

服务的 `masterBranch` 为必填远程分支。模块 `masterBranch: null` 实时继承服务值，填写 `<来源 remote>/<branch>` 时独立覆盖；模块 `tagTargetRef: null` 独立继承服务的 `testTagBaselineRef`。创建任务时把最终分支写入任务清单快照，之后修改服务配置不会改变已有任务。每个模块都会使用稳定的 `服务名-模块名` 目录。模块 ID、名称和目录名忽略大小写不得重复；新建独立克隆会将所选来源 URL 命名为自身的 `origin`，后续 Push、恢复和 Git 操作仍统一使用 `origin`。

`meegleExecutablePath` 为 `null` 时，应用会通过平台 login shell 自动探测 Meegle CLI 并缓存结果；也可以在设置页填写已存在、可执行的绝对路径。探测失败时回退到 PATH 中的 `meegle.cmd`（Windows）或 `meegle`（macOS/Linux）。

`larkExecutablePath` 为 `null` 时，应用会优先通过 `where.exe lark-cli.cmd`（Windows）或平台 login shell 探测 Lark CLI，并将首次成功识别到的绝对路径写回配置；也可以在设置页填写已存在、可执行的绝对路径，探测失败时回退到 PATH 中的 `lark-cli.cmd`（Windows）或 `lark-cli`（macOS/Linux）。登录时每次手动选择业务域，授权链接和设备码只在内存中短暂存在。

`genbuExecutablePath` 为 `null` 时，应用通过 `where.exe genbu.exe`（Windows）或平台 shell 自动探测，并会将首次成功识别到的绝对路径自动写回配置。填写已存在、可执行的绝对路径后，该路径只在自动探测未找到 Genbu 时作为兜底使用；自动识别到的命令始终优先。

## 任务资料目录

设置页的“任务资料目录设置”包含两个由用户自行填写的字段：`requirementMaterialsRoot` 是保存根路径，保存时会转换为绝对规范路径并创建目录；`requirementMaterialsSubdirectory` 是每个需求目录下的单层子目录名，保存时会去除首尾空格。子目录名不得包含 Windows 路径分隔符、非法字符、`.`/`..`、结尾点或空格，也不能使用 `CON`、`PRN`、`AUX`、`NUL`、`COM1`–`COM9`、`LPT1`–`LPT9` 等保留名。

两个字段任意一个为空时，任务资料目录功能均视为未配置，不会隐式使用默认路径或默认子目录；创建任务表单、`AGENTS.md` 预览和实际创建过程也不会展示、查询或记录资料目录失败。配置有效且创建任务时填写需求编号或飞书需求链接后，创建页会先进行无写入的路径预检，显示完整预计路径以及“预计新建”或“将复用”；真正创建任务时仍会重新校验并创建或复用任务资料目录。

## 任务资料根与 Agent 过程文档

`requirementMaterialsRoot` 是唯一的任务资料根目录，`requirementMaterialsSubdirectory` 是每个需求目录下的资料子目录（例如“研发”）。两个字段都必须由用户填写；任意一个为空、路径不合法或子目录名不安全时，任务资料功能均视为未配置，不会创建隐式默认目录。

配置有效且创建任务时填写需求编号或飞书需求链接后，桌面端会创建或复用：

```text
<requirementMaterialsRoot>/<Sprint>/<需求编号>-<任务文件夹名>/<requirementMaterialsSubdirectory>
```

桌面端普通任务只创建上述资料目录，不创建过程文档。`silverwing agent plan/apply` 复用同一任务资料目录，并在其 `write_root`（上式最后的资料子目录）内补写 `.silverwing-requirement.json`、`00-需求总览.md` 等过程文档；Sprint 层的 `.silverwing-iteration.json`、`00-迭代任务总览.md` 保留在资料根下。需求目录名始终使用任务文件夹名，Agent 请求中的需求标题仅作为 Markdown 标题。

已存在且唯一的需求目录会复用；如果递归查找到多个 `<需求编号>` 或 `<需求编号>-*` 目录，操作会明确失败，不自动选择。发现已有过程文档 manifest 时会校验需求身份，身份不一致则停止写入。silverwing 不移动、删除或自动迁移历史任务资料目录，任务交接文件位于任务目录的 `.workspace/HANDOFF.md`。

配置分片严格使用 `schema: 6`，应用配置版本为 `6.0.0`；任务清单仍使用 `2.0.0`。silverwing 不读取、迁移或改写旧 schema 配置。需要保存或转移当前 silverwing 配置时，请在设置页导出完整 ZIP，再在目标环境验证后导入。

## 组

- 配置始终至少有一个组；只有一个组时，任务和服务页面隐藏组选择与折叠层级。
- 多组时，任务和服务按组折叠展示。
- 设置页可创建、重命名、排序组；只有空组可以删除，且必须保留至少一个组。
- 一个仓库可以加入多个组，但同一组内只能出现一次。
- 组内服务也使用数组保存并支持排序。
- 全局 `defaultBranchPrefix` 可包含唯一占位符 `{num}`。飞书链接优先使用工作项 ID；其他 URL 忽略 query/fragment 后从 path 取最后一段数字，普通文本取最后一段数字。无法解析时必须手工修正分支后才能创建。

## 人工添加仓库

设置页通过操作系统原生目录选择器一次选择一个或多个仓库目录。验证过程会：

1. 解析所选路径所属的 Git 顶层目录；
2. 获取规范化 `git-common-dir` 作为物理仓库身份；
3. 拒绝非 Git 目录、Bare 仓库、子模块和临时 Linked Worktree；
4. 读取当前分支及 `origin`，拒绝 URL 中嵌入的明文凭据；
5. 每个目录独立校验，不递归扫描其子目录、父目录或同级目录；
6. 合法仓库批量写入一次配置，新增服务默认采用标准 Worktree，之后可在服务配置中修改策略。

应用启动不会重新校验全部仓库或访问远端；只对当前任务执行本地只读 Git 状态检查。用户点击顶部“刷新”后才重新校验已经配置的仓库。

当前任务会异步运行 `git status --porcelain=v2 -z --untracked-files=all`，展示包括未跟踪文件在内的未提交文件数；同时只比较本地已知 upstream 与 `HEAD`，不会为了状态展示执行 Fetch 或访问远端。

## 服务工作区策略

### 标准 Worktree

标准服务至少有一个模块。服务指定主分支；模块主分支默认继承服务，也可独立覆盖。合并模式的测试 Tag 目标同样默认继承服务，可独立覆盖为 `<remote>/<branch>`，例如 `origin/release/test`：

- 每个模块都创建独立 Worktree，即使多个模块使用相同主分支；
- 单模块保留用户输入的任务分支名；
- 多模块按模块名自动添加后缀，例如 `feature/ABC-api`、`feature/ABC-jobs/nightly`；创建页可以分别覆盖每个模块的本次主分支和目标分支；
- 模块名只允许英文字母、数字、`-`、`_`、`/`，忽略大小写后不能重复；目录名会将 `/` 转为 `-`，转换后也不能冲突。

创建前会执行 `fetch --prune --no-tags <remote>`，并从最新的 `refs/remotes/<remote>/<branch>` 创建 Worktree；不会切换或移动用户本地 `master`。普通任务创建不受本地同名 Tag 冲突影响。标准服务创建 Worktree 后按服务配置执行 Bootstrap。

每次启动后，silverwing 会在后台静默补齐 `developmentTools` 中仍未配置的工具，不阻塞界面、不弹窗、不联网，也不会递归扫描磁盘。已有配置即使路径已经失效也不会被覆盖。Windows 依次检查 Program Files、`%LOCALAPPDATA%\Programs`、JetBrains Toolbox 稳定版目录和 `where.exe` 可解析的 PATH；macOS 依次检查 `/Applications`、`~/Applications`、JetBrains Toolbox 稳定版目录和 PATH 中的命令。探测完成时会重新读取配置，并通过一次原子更新只补仍为空的类型，因此不会覆盖探测期间用户手动保存的路径。支持 IntelliJ IDEA、WebStorm、PyCharm、Visual Studio Code、Android Studio 和 DevEco Studio；未找到的类型保持为空。

`allowTemporaryDevelopmentToolSelection` 默认关闭。关闭时，任务工具栏和工作区行只用各自默认开发工具打开；开启后才显示临时 IDE 下拉。该开关不会让 silverwing 在任务创建完成后自动打开服务。

### 独立克隆

独立克隆模块默认继承服务主分支，也可以单独配置主分支；创建任务时还可临时覆盖。它从主分支对应远程的 URL 完整克隆，并在新目录中将该来源命名为 `origin`；随后直接切到该分支，不创建额外 Feature 分支或 Linked Worktree。创建与恢复后执行 Bootstrap，归档和删除仍会进行 Git 安全检查。

## Tag 开关与模式

有效 Tag 入口需要同时满足：

1. 任务所属组的 `tagEnabled` 已开启；
2. 标准模块或独立克隆模块的 `tagEnabled` 已开启。

`MERGE_TO_TARGET_BRANCH` 会把当前分支安全合并并推送到 `tagTargetRef` 后，在目标提交上创建 Tag。`CURRENT_BRANCH` 不需要目标分支，会先把当前分支非强制推送到任务记录的 `pushRemote`，再直接在当前 HEAD 创建 Tag。独立克隆始终使用任务中实际克隆的分支。

## 分片 AGENTS.md

任务说明由三个 Markdown 文件组成。任务根目录的 `AGENTS.md` 包含读取顺序、全局与组规则的实际路径及规则优先级；它不会包含需求、服务、Worktree 或人工说明，也不会随着这些内容变化而重写。

```text
<任务目录>/
  AGENTS.md
  silverwing.json
  .workspace/
    HANDOFF.md
    agent/
      TASK-CONTEXT.md
      TASK-RULES.md
```

`TASK-CONTEXT.md` 记录需求、资料目录、交接和任务清单说明，并在“Worktree 范围”中列出可修改 Worktree 与只读仓库；`TASK-RULES.md` 是唯一可直接编辑的任务专属规则。共享规则只保留路径引用，不复制正文。规则优先级为任务专属规则、组规则、全局规则。

任务中的“需求说明”直接展示这三个文件，默认选中 `TASK-RULES.md`。仅该文件显示阅读、编辑和保存操作；另外两个文件只读，支持源码、渲染、目录导航和复制。编辑期间切换文件会保留未保存草稿，返回 `TASK-RULES.md` 可继续编辑。

应用使用 WatchService、窗口聚焦补检、内容哈希、防抖和原子写入同步可编辑规则文件；外部修改与未保存编辑冲突时必须由用户选择磁盘版本或本地版本。普通任务更新只写对应分片，不会覆盖根路由文件；仅显式刷新 Agent 文档时才会补建缺失的根路由。

## Bootstrap

Bootstrap 是服务级快照，对该服务新创建的每个 Worktree 或独立克隆模块执行。复制规则必须使用明确的相对路径，禁止绝对路径、`..`、`.git` 和符号链接穿越。命令按声明顺序运行，单步失败会记录警告并继续后续步骤，最终工作区标记为 `READY_WITH_WARNINGS`。

## 任务工作区工具与任务 schema

`silverwing.json` 使用严格字符串 schema，当前写入版本为 `"2.0.0"`。创建任务时会继承全局 `defaultWorkspaceToolIds`，用户可以在创建页增减。任务本身创建成功后，工具适配器逐项打开；其中一个失败不会回滚 Git 工作区，也不会阻止其他工具。silverwing 不读取、迁移或删除旧产品的配置和任务清单。

```json
{
  "schemaVersion": "2.0.0",
  "lifecycleStatus": "ACTIVE",
  "services": [
    {
      "serviceName": "order-service",
      "moduleId": "default",
      "moduleName": "default",
      "strategy": "STANDARD_WORKTREE",
      "moduleSource": "CONFIGURED",
      "baseRef": "origin/master",
      "targetBranch": "feature/123-default",
      "health": "READY",
      "branchCreatedByTask": false,
      "forceWorktreeAttach": true
    }
  ],
  "workspaceToolLaunches": [
    {
      "toolId": "codex",
      "status": "OPENED",
      "updatedAt": "2026-01-01 00:00:00",
      "message": null
    }
  ]
}
```

`lifecycleStatus` 只表示任务属于活跃还是已归档；每个服务的 `health` 只表示工作区是否可用。任务整体健康度由服务动态聚合，不会作为第三个状态字段写入 JSON。`branchCreatedByTask` 标记本次任务是否创建了本地分支，失败回滚只会删除该类分支；`forceWorktreeAttach` 标记恢复时是否需要以 `git worktree add --force` 再次附加已被其他 Worktree 检出的分支。

`blockedGitWriteBranches` 按完整本地分支名忽略大小写匹配，默认保护 `master`、`main`，不支持通配符。受保护分支仍可作为主分支被检出，但 silverwing 会在任何写入前阻止 Commit、Push、Commit & Push，以及需要写入该分支的 Tag 流程。

未注册的工具 ID 会原样保留在配置中并在界面显示为“当前不可用”。Core 只认识通用工具 ID 和执行结果，不依赖 Codex、Claude、Cursor 的 URI 或命令。

silverwing 生成的任务、Tag 操作、历史记录、JSONL 事件和 AGENTS.md 时间统一使用 `Asia/Shanghai` 时区，格式为 `yyyy-MM-dd HH:mm:ss`。这不会重写 Git 提交时间、远程 Tag 原始时间或文件系统修改时间。
