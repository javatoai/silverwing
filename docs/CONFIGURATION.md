# 配置与使用

## 配置目录

Windows 使用 `%USERPROFILE%\awm`，macOS 使用 `~/awm`。首次启动时，如果 `config.json` 不存在，AWM 会创建应用目录和 `tasks` 子目录，并原子写入以 `~/awm/tasks` 为 `taskRoot` 的当前版本配置。默认任务目录无法创建时，AWM 会保留一份可继续编辑的当前版本配置、显示错误，并允许在设置中选择其他可写目录。已有配置中的自定义 `taskRoot` 保持不变；程序不会探测或读取旧的 `~/.AgentWorkspaceManager`。

1.0.x 的说明文件固定保存在：

```text
agents/global/AGENTS.md
agents/groups/<groupId>/AGENTS.md
agents/task-templates.json
```

磁盘文件是唯一可信来源；说明正文不重复保存在 `config.json`。

## 严格数组 schema

1.0.x 的 `config.json` 使用严格字符串 schema，当前写入版本为 `"1.0.9"`。顶层仓库和组均为数组，数组顺序就是界面顺序：

```json
{
  "schemaVersion": "1.0.9",
  "taskRoot": "C:\\Users\\alice\\awm\\tasks",
  "developmentTools": [
    { "type": "INTELLIJ_IDEA", "path": "C:\\Tools\\idea64.exe" },
    { "type": "VISUAL_STUDIO_CODE", "path": "C:\\Tools\\Code.exe" }
  ],
  "defaultDevelopmentTool": "INTELLIJ_IDEA",
  "allowTemporaryDevelopmentToolSelection": false,
  "blockedGitWriteBranches": ["master", "main"],
  "terminalExecutable": "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
  "requirementMaterialsRoot": null,
  "requirementMaterialsSubdirectory": null,
  "meegleExecutablePath": null,
  "genbuExecutablePath": null,
  "repositories": [
    {
      "id": "repo-…",
      "name": "order-service",
      "rootPath": "Q:\\services\\order-service",
      "gitCommonDirectory": "Q:\\services\\order-service\\.git",
      "originUrl": "git@github.com:example/order-service.git",
      "currentBranch": "master",
      "defaultRemoteBranch": "master"
    }
  ],
  "groups": [
    {
      "id": "default",
      "name": "默认组",
      "tagEnabled": true,
      "defaultBranchPrefix": "feature/zhangsan_{num}_",
      "defaultWorkspaceToolIds": ["codex"],
      "services": [
        {
          "id": "service-order",
          "repositoryId": "repo-…",
          "displayName": "订单服务",
          "enabled": true,
          "genbuProbeEnabled": false,
          "genbuServiceName": "service-order",
          "developmentTool": "INTELLIJ_IDEA",
          "commitMessageTemplate": "feat: {num} 完成开发",
          "modules": [
            {
              "id": "default",
              "name": "default",
              "strategy": "STANDARD_WORKTREE",
              "baseRef": "origin/master",
              "baseRemote": "origin",
              "tagEnabled": true,
              "tagMode": "MERGE_TO_TARGET_BRANCH",
              "tagTargetRef": "origin/release/test",
              "tagMessagePrefix": "Tag"
            },
            {
              "id": "reporting",
              "name": "reporting",
              "strategy": "INDEPENDENT_CLONE",
              "baseRef": "origin/develop",
              "baseRemote": "origin",
              "tagEnabled": false,
              "tagMode": "CURRENT_BRANCH",
              "tagTargetRef": null,
              "tagMessagePrefix": "Tag"
            }
          ],
          "bootstrap": {
            "copyRules": [],
            "commands": []
          }
        }
      ]
    }
  ],
  "theme": "SYSTEM",
  "tagEnabled": true,
  "tagHistoryMaxGroups": 20
}
```

## Tag设置

`tagEnabled` 是全局 Tag 开关，默认值为 `true`。关闭后，桌面端会隐藏 Tag 导航、工作区构建入口、组和模块中的 Tag 配置，以及 Tag Skill 入口；Tag 设置本身仍保留，方便重新开启。核心用例和 `awm tag` CLI 也会拒绝新的 Tag 操作。

`tagHistoryMaxGroups` 控制本地 Tag 构建历史最多保留的组数，默认值为 `20`，允许范围为 `1`–`1000`。单次批量构建产生的记录属于同一组；超过上限时，AWM 按组的最近更新时间从旧到新清理整组记录，只删除任务目录中的 Tag 操作记录和历史汇总，不删除 Git Tag、代码或任务。应用启动读取历史、任务列表变化、Tag 构建完成或保存该设置后都会执行清理。

## 任务根目录迁移

在设置页选择新的任务根目录后，AWM 先进行只读预检。旧目录没有任务时直接保存；存在任务时展示迁移方式、任务数、工作区数和数据量，用户确认后才开始迁移。

- 新旧目录不能相同或互相包含；目标必须为空且可写，并有足够空间。
- 所有任务清单必须可读且为当前版本，工作区必须位于对应任务目录内；发现冲突会阻止整批迁移。
- 同磁盘整体移动任务目录；跨磁盘不跟随符号链接地复制并保留基础文件属性。
- 标准 Worktree 执行 `git worktree repair`；独立克隆保留完整 `.git`。
- AWM 校验 HEAD、分支、仓库身份以及暂存、未暂存和未跟踪文件状态，更新清单路径并重新生成 `AGENTS.md` 系统区。
- 全部任务成功后才原子更新 `taskRoot` 并清理旧目录。清理失败时新目录仍生效，界面会列出待清理路径；迁移日志位于 `~/awm/migrations/task-root.json`，下次启动会继续清理或回滚。

需求资料目录由 `requirementMaterialsRoot` 独立管理，不随任务根目录迁移。

工作区策略属于模块而不是服务。同一服务的 `modules` 可以同时包含 Worktree 与独立克隆模块：

`genbuProbeEnabled` 是服务级开关，默认 `false`。开启后，Tag 构建页面会用 `where.exe genbu.exe` 自动发现本机 `genbu` CLI，并在页面打开期间以 `genbu query-tag --json <服务> <精确 Tag>` 查询所有带 Tag 记录的构建、UAT 发版和生产发版三个阶段的状态（初始/构建中/成功/失败）及其返回的完成时间；`genbuServiceName` 默认使用服务展示名称，可在服务配置中改为 Genbu 的实际服务名。自动轮询会跳过已完成 UAT 发布、构建失败、被更晚 Tag 覆盖或已确认未在 Genbu 找到的记录；页面的“刷新 Genbu”会强制重新查询全部带 Tag 记录。当某条记录的 Genbu 构建结果为失败时，行内提供“重新打Tag”按钮：重新执行完整 Tag 流程，版本号在失败 Tag 基础上自动 +1，并原地替换该条构建记录。

```json
"modules": [
  {
    "id": "feign-master",
    "name": "主线客户端",
    "strategy": "STANDARD_WORKTREE",
    "baseRef": "origin/master",
    "baseRemote": "origin",
    "tagEnabled": true,
    "tagMode": "MERGE_TO_TARGET_BRANCH",
    "tagTargetRef": "origin/release/test",
    "tagMessagePrefix": "Tag"
  },
  {
    "id": "feign-development",
    "name": "开发客户端",
    "strategy": "INDEPENDENT_CLONE",
    "baseRef": "origin/development",
    "baseRemote": "origin",
    "tagEnabled": false,
    "tagMode": "CURRENT_BRANCH",
    "tagTargetRef": null,
    "tagMessagePrefix": "Tag"
  }
]
```

每个模块都会使用稳定的 `服务名-模块名` 目录。模块 ID、名称和目录名忽略大小写不得重复；独立克隆模块可以选择原仓库的任意远程作为来源，基础 Ref 使用 `<来源 remote>/<branch>` 格式。新建 clone 会将所选来源 URL 命名为自身的 `origin`，因此后续 Push、恢复和 Git 操作仍统一使用 `origin`。

`meegleExecutablePath` 为 `null` 时，应用会通过平台 login shell 自动探测 Meegle CLI 并缓存结果；也可以在设置页填写已存在、可执行的绝对路径。探测失败时回退到 PATH 中的 `meegle.cmd`（Windows）或 `meegle`（macOS/Linux）。

`genbuExecutablePath` 为 `null` 时，应用通过 `where.exe genbu.exe`（Windows）或平台 shell 自动探测，并会将首次成功识别到的绝对路径自动写回配置。填写已存在、可执行的绝对路径后，该路径只在自动探测未找到 Genbu 时作为兜底使用；自动识别到的命令始终优先。

## 需求资料目录

设置页的“需求资料目录设置”包含两个由用户自行填写的字段：`requirementMaterialsRoot` 是保存根路径，保存时会转换为绝对规范路径并创建目录；`requirementMaterialsSubdirectory` 是每个需求目录下的单层子目录名，保存时会去除首尾空格。子目录名不得包含 Windows 路径分隔符、非法字符、`.`/`..`、结尾点或空格，也不能使用 `CON`、`PRN`、`AUX`、`NUL`、`COM1`–`COM9`、`LPT1`–`LPT9` 等保留名。

两个字段任意一个为空时，需求资料目录功能均视为未配置，不会隐式使用默认路径或默认子目录；创建任务表单、`AGENTS.md` 预览和实际创建过程也不会展示、查询或记录资料目录失败。配置有效且创建任务时填写需求编号或飞书需求链接后，创建页会先进行无写入的路径预检，显示完整预计路径以及“预计新建”或“将复用”；真正创建任务时仍会重新校验并创建或复用需求资料目录。

## 需求资料根与 Agent 过程文档

`requirementMaterialsRoot` 是唯一的需求资料根目录，`requirementMaterialsSubdirectory` 是每个需求目录下的资料子目录（例如“研发”）。两个字段都必须由用户填写；任意一个为空、路径不合法或子目录名不安全时，需求资料功能均视为未配置，不会创建隐式默认目录。

配置有效且创建任务时填写需求编号或飞书需求链接后，桌面端会创建或复用：

```text
<requirementMaterialsRoot>/<Sprint>/<需求编号>-<任务文件夹名>/<requirementMaterialsSubdirectory>
```

桌面端普通任务只创建上述资料目录，不创建过程文档。`awm agent plan/apply` 复用同一需求资料目录，并在其 `write_root`（上式最后的资料子目录）内补写 `.awm-requirement.json`、`00-需求总览.md` 等过程文档；Sprint 层的 `.awm-iteration.json`、`00-迭代任务总览.md` 保留在资料根下。需求目录名始终使用任务文件夹名，Agent 请求中的需求标题仅作为 Markdown 标题。

已存在且唯一的需求目录会复用；如果递归查找到多个 `<需求编号>` 或 `<需求编号>-*` 目录，操作会明确失败，不自动选择。发现已有过程文档 manifest 时会校验需求身份，身份不一致则停止写入。AWM 不移动、删除或自动迁移历史资料目录，`.awm/HANDOFF.md` 仍位于任务目录中。

未知字段，以及主版本或次版本不同的 schema 都会被拒绝，应用不会自动迁移或改写原文件。同一主次版本的 PATCH 版本可直接读取，并在下一次正常保存时更新为当前 PATCH。0.12.x 及更早版本的配置与任务清单不会被 1.0.x 读取、迁移或删除；旧的独立过程文档目录保持原样。升级时请先备份用户数据，手工移除旧配置中的 `requirementDocumentationRoot`，将 schema 改为 `1.0.9`，然后在设置页重新保存需求资料根目录与子目录。

## 组

- 配置始终至少有一个组；只有一个组时，任务和服务页面隐藏组选择与折叠层级。
- 多组时，任务和服务按组折叠展示。
- 设置页可创建、重命名、排序组；只有空组可以删除，且必须保留至少一个组。
- 一个仓库可以加入多个组，但同一组内只能出现一次。
- 组内服务也使用数组保存并支持排序。
- `defaultBranchPrefix` 可包含唯一占位符 `{num}`。飞书链接优先使用工作项 ID；其他 URL 忽略 query/fragment 后从 path 取最后一段数字，普通文本取最后一段数字。无法解析时必须手工修正分支后才能创建。

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

标准服务至少有一个模块。每个模块指定 `baseRemote`、基础 Ref、Tag 模式和 Tag 子开关。合并模式的 `tagTargetRef` 使用 `<remote>/<branch>` 格式，例如 `origin/release/test`：

- 每个模块都创建独立 Worktree，即使多个模块使用相同基础 Ref；
- 单模块保留用户输入的任务分支名；
- 多模块按模块名自动添加后缀，例如 `feature/ABC-api`、`feature/ABC-jobs/nightly`；创建页可以分别覆盖每个模块的基础分支和目标分支；
- 模块名只允许英文字母、数字、`-`、`_`、`/`，忽略大小写后不能重复；目录名会将 `/` 转为 `-`，转换后也不能冲突。

创建前会执行 `fetch --prune --no-tags <remote>`，并从最新的 `refs/remotes/<remote>/<branch>` 创建 Worktree；不会切换或移动用户本地 `master`。普通任务创建不受本地同名 Tag 冲突影响。标准服务创建 Worktree 后按服务配置执行 Bootstrap。

每次启动后，AWM 会在后台静默补齐 `developmentTools` 中仍未配置的工具，不阻塞界面、不弹窗、不联网，也不会递归扫描磁盘。已有配置即使路径已经失效也不会被覆盖。Windows 依次检查 Program Files、`%LOCALAPPDATA%\Programs`、JetBrains Toolbox 稳定版目录和 `where.exe` 可解析的 PATH；macOS 依次检查 `/Applications`、`~/Applications`、JetBrains Toolbox 稳定版目录和 PATH 中的命令。探测完成时会重新读取配置，并通过一次原子更新只补仍为空的类型，因此不会覆盖探测期间用户手动保存的路径。支持 IntelliJ IDEA、WebStorm、PyCharm、Visual Studio Code、Android Studio 和 DevEco Studio；未找到的类型保持为空。

`allowTemporaryDevelopmentToolSelection` 默认关闭。关闭时，任务工具栏和工作区行只用各自默认开发工具打开；开启后才显示临时 IDE 下拉。该开关不会让 AWM 在任务创建完成后自动打开服务。

### 独立克隆

独立克隆服务必须保存默认来源远程和基础分支，创建任务时可以覆盖。它从所选来源远程的 URL 完整克隆，并在新目录中将该来源命名为 `origin`；随后直接切到该分支，不创建额外 Feature 分支或 Linked Worktree。创建与恢复后执行 Bootstrap，归档和删除仍会进行 Git 安全检查。

## Tag 开关与模式

有效 Tag 入口需要同时满足：

1. 任务所属组的 `tagEnabled` 已开启；
2. 标准模块或独立克隆模块的 `tagEnabled` 已开启。

`MERGE_TO_TARGET_BRANCH` 会把当前分支安全合并并推送到 `tagTargetRef` 后，在目标提交上创建 Tag。`CURRENT_BRANCH` 不需要目标分支，会先把当前分支非强制推送到任务记录的 `pushRemote`，再直接在当前 HEAD 创建 Tag。独立克隆始终使用任务中实际克隆的分支。

## 三级 AGENTS.md

最终任务说明按以下层级合成：

1. 系统生成的任务与工作区信息；
2. 全局说明；
3. 组说明；
4. 任务人工说明。

冲突时任务级优先，其次是组级，再其次是全局。任务文件用以下协议分隔系统区和人工区：

```text
<!-- AWM:GENERATED:BEGIN -->
…系统生成内容…
<!-- AWM:GENERATED:END -->

<!-- AWM:TASK-NOTES:BEGIN -->
…用户可编辑内容…
<!-- AWM:TASK-NOTES:END -->
```

重新生成只替换系统区并保留人工区。应用使用 WatchService、窗口聚焦补检、内容哈希、防抖和原子写入同步文件；外部修改与未保存编辑冲突时必须由用户选择磁盘版本或本地版本。标记缺失或重复时会停止自动覆盖，等待用户修复。

## Bootstrap

Bootstrap 是服务级快照，对该服务新创建的每个 Worktree 或独立克隆模块执行。复制规则必须使用明确的相对路径，禁止绝对路径、`..`、`.git` 和符号链接穿越。命令按声明顺序运行，单步失败会记录警告并继续后续步骤，最终工作区标记为 `READY_WITH_WARNINGS`。

## 任务工作区工具与任务 schema

`agent-workspace.json` 使用严格字符串 schema，当前写入版本为 `"1.0.9"`。创建任务时会继承所属组的 `defaultWorkspaceToolIds`，用户可以在创建页增减。任务本身创建成功后，工具适配器逐项打开；其中一个失败不会回滚 Git 工作区，也不会阻止其他工具。1.0.x 不读取、迁移或删除 0.12.x 及更早版本的配置和任务清单。

```json
{
  "schemaVersion": "1.0.9",
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

`blockedGitWriteBranches` 按完整本地分支名忽略大小写匹配，默认保护 `master`、`main`，不支持通配符。受保护分支仍可作为基础分支被检出，但 AWM 会在任何写入前阻止 Commit、Push、Commit & Push，以及需要写入该分支的 Tag 流程。

未注册的工具 ID 会原样保留在配置中并在界面显示为“当前不可用”。Core 只认识通用工具 ID 和执行结果，不依赖 Codex、Claude、Cursor 的 URI 或命令。

AWM 生成的任务、Tag 操作、历史记录、JSONL 事件和 AGENTS.md 时间统一使用 `Asia/Shanghai` 时区，格式为 `yyyy-MM-dd HH:mm:ss`。这不会重写 Git 提交时间、远程 Tag 原始时间或文件系统修改时间。
