# 安全与 Tag 流程

## 预检

每个服务构建前会：

1. 检查 Feature Worktree 没有暂存、未提交、未跟踪或进行中的 Git 操作。
2. 先以 `fetch --prune --no-tags` 刷新分支，再以远端为权威单独同步 Tag；同名 Tag 会以远端提交覆盖，远端已删除的本地 Tag 会被清理。
3. 比较本地和远端 Feature 分支；远端领先或已分叉时停止，不自动 pull/rebase。
4. 合并模式解析目标分支的精确 SHA；当前分支模式不需要目标分支。
5. 合并模式在 `~/silverwing/temp/tag-build` 下创建 Detached 临时 Worktree。
6. 合并模式执行不提交的合并检测，收集冲突文件并清理临时 Worktree。
7. 桌面端展示合并方式、提交列表、Diff Stat 和预计 Tag，等待人工确认。

## 正式构建

桌面端在人工确认预检结果后、CLI 在预检通过后立即：

1. 必要时推送当前分支并设置 Upstream。
2. 合并模式重新 Fetch，基于最新远端目标分支创建临时 Worktree。
3. 当前分支已包含在目标分支时直接进入 Tag 阶段；当前分支模式直接使用当前 HEAD。
4. 可 Fast-forward 时允许 Fast-forward；分叉时创建普通 Merge Commit。
5. 使用普通、非强制 Push 更新目标分支。
6. 校验远端目标分支 SHA 与本次合并 SHA 完全一致。
7. 在目标提交或当前 HEAD 创建 Annotated Tag。
8. 推送 Tag，并处理同名 Tag 竞态；最多重新计算并重试一次。
9. 输出可复制的 `服务：Tag` 清单。

Tag 同步的强制覆盖只作用于本地 Tag 引用，不会修改远端 Tag。工具明确不会执行：

- Force Push
- 自动 Pull / Rebase
- 自动解决冲突
- 修改用户正在开发的 Worktree
- 删除远端分支或远端 Tag

## 执行入口

桌面端与 `silverwing tag` CLI 共用同一套应用层 Tag 用例，上述预检、写策略、仓库锁、非强制 Push 和状态机完全一致，差别只在确认方式：

- 桌面端分两步：先预检并展示合并方式、提交列表、Diff Stat 和预计 Tag，人工确认后才执行正式构建。
- CLI 一步直接执行：`silverwing tag build` 在预检通过后立即构建，不展示待确认预览。它面向 AI 调用，失败以记录形式返回失败原因、冲突分支与冲突文件，以及下一步该执行的命令。

两个入口都不会绕过预检：脏工作区、远端领先或已分叉、合并冲突同样会停止构建并落为可重试的记录。CLI 的闭环命令为 `silverwing tag build|status|history|retry|workspace-check`；`retry` 按记录状态自动选择重试路径，始终复用原记录，不产生重复历史行。命令细节见 `skills/silverwing/references/tag-builds.md`。

## Tag 规则

三级开关同时通过才允许 Tag：全局 `tagEnabled` 为开，任务所属组的 `tagEnabled` 为开，且对应标准模块或独立克隆模块的 `tagEnabled` 为开。全局关闭时桌面端隐藏 Tag 相关入口，CLI 和核心用例也会拒绝 Tag 操作；全局开启后再按组和模块开关判断。独立克隆以任务中模块配置的固定分支参与 Tag，不会重新派生 Feature 分支。

Tag 历史按批量构建分组，`tagHistoryMaxGroups` 默认保留 20 组，超过上限时按最近更新时间清理最旧的整组本地记录。清理只处理任务目录中的 Tag 历史文件，不删除 Git Tag、代码或任务。

有效格式：

```text
X.Y.Z
X.Y.Z.N
X.Y.Z.beta-N
```

递增规则与现有 `auto-build-uat-tag.ps1` 兼容：

```text
1.6.89.beta-9 → 1.6.89.beta-10
1.6.89.7      → 1.6.90.beta-1
1.6.89        → 1.6.90.beta-1
```

多条 major/minor 版本线并存时，同样按照参考脚本的规则先结合 Tag 创建时间确定活跃版本线，
再在该版本线内按版本号选择最新 Tag，避免被仍保留在仓库中的旧版本线误导。

仓库没有符合上述规则且可从目标提交访问的历史 Tag 时，Tag 预检会直接阻断。请先在仓库创建并推送一个符合规则的 Tag，再由 silverwing 计算后续版本。

## 冲突处理

发生冲突时，工具会显示冲突文件并清理临时 Worktree。用户应：

1. 手工将 Feature 分支合并到配置的测试分支。
2. 解决冲突并推送测试分支。
3. 桌面端回到任务页重新预检并构建；CLI 执行 `silverwing tag retry --task <任务文件夹名> --operation <operationId>`。

重新构建会识别 Feature 已是测试分支祖先，不重复制造 Merge Commit，而是基于最新测试分支生成 Tag。

## 状态恢复

每次操作保存在：

```text
<task>/tag-operations/<operationId>.json
```

终态追加到：

```text
<task>/tag-build-history.jsonl
```

状态包括：

```text
CREATED
PREFLIGHT_PASSED
SOURCE_BRANCH_PUSHED
TARGET_BRANCH_PUSHED
LOCAL_TAG_CREATED
TAG_PUSHED
SUCCESS
CONFLICT
FAILED
PARTIAL
```

目标分支已推送或本地 Tag 已创建、但 Tag 推送失败时标记为 `PARTIAL`。桌面端“继续构建Tag”
与 CLI `silverwing tag retry` 都复用相同本地 Tag，不会错误地跳到下一个版本。

## 归档

安全归档会检查每个标准 Worktree 或独立克隆工作区：

- Staged
- Unstaged
- Untracked
- 当前分支未推送提交
- Merge / Rebase / Cherry-pick / Revert

只有全部安全时才移除工作区。标准 Worktree 通过 Git 安全移除，独立克隆则在路径与状态复核后删除；分支、任务清单和 Tag 历史会保留。
强制归档必须输入完整 `taskKey`；它可能丢弃未保存的本地文件，应谨慎使用。
