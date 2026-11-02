# LayerFS 原理（当前 main 源码快照）

核验日期：2026-10-22
仓库：Ephemeral-AI-Lab/layerfs
main commit：[2e5061798d235819be45ef655e37ffa2799376d0](https://github.com/Ephemeral-AI-Lab/layerfs/commit/2e5061798d235819be45ef655e37ffa2799376d0)

以下结论来自该 commit 的官方源码静态查阅；没有安装或运行项目。解释以当前 SDK 和 core 为准。

## 一句话理解

LayerFS 把已保存的文件系统表示成不可变内容对象组成的图；每个 Workspace 绑定只读基线，并把改动暂存在自己的 overlay 中。读取时叠加基线和局部改动，提交时保存新内容对象及文件系统根，再依据绑定时的基线与最新分支头做三方合并。

## 对象、分块与写时复制

- 对象 ID 是 32 字节 BLAKE3 摘要，输入为固定 domain separator 加完整 canonical object bytes；对象字节有 magic、kind、长度和值等 framing。因此它是规范化对象的身份，不等于直接对原始文件字节做 hash。相同规范化对象得到相同 ID。[object/id.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/object/id.rs#L1-L35) [object/codec.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/object/codec.rs#L1-L6)
- 大文件按冻结的 streaming GEAR CDC 切块：最小 8 KiB、目标 16 KiB、最大 32 KiB。文件的 extent tree 把逻辑偏移映射到 chunk 对象的 payload 切片；分支节点指向子节点并记录累计范围。文件根、映射页和 chunks 通过对象 ID 互相引用，结构类似 Merkle DAG（这是根据数据结构作出的描述，源码没有把它命名为 Merkle tree）。[cdc/gear.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/file/cdc/gear.rs#L1-L21) [mapping/types.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/file/mapping/types.rs#L1-L6) [mapping/types.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/file/mapping/types.rs#L25-L32) [mapping/types.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/file/mapping/types.rs#L70-L100)
- Store 的 SQLite 发布逻辑按 object_id 建位置索引，重复 ID 走 ON CONFLICT(object_id) DO NOTHING，说明同一 Store 不会为同一对象身份重复登记一份位置。[sqlite/publish.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-persistence/src/backend/sqlite/publish.rs#L7-L50)
- 文件编辑从不可变基线读取并构造新根；如果替换内容与基线逐字节相同，编辑器直接返回原根。Chunked 文件的保留区间与替换数据会进入相同的规范化构造流程，所以改动以新对象/映射表示，未改变且身份相同的对象可以复用。CDC 边界附近是否重切由构造器决定，不能简单理解成只重写用户写到的那几个字节。[edit/apply.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/file/edit/apply.rs#L1-L9) [edit/apply.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/file/edit/apply.rs#L200-L228)

## Workspace overlay、读写和 Commit

- Workspace 的有效视图由 immutable base 和 overlay jobs 组成。FUSE 读取先取 overlay window；如果该范围仍继承自 base，才按记录的 base root 向 Store 读取。[workspace/view.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-workspace/src/workspace/view.rs#L1-L4) [fuse/operations/read.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-fuse/src/operations/read.rs#L31-L36) [fuse/operations/read.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-fuse/src/operations/read.rs#L70-L85)
- 一次文件写入生成局部 payload write 和 inode 状态；未覆盖的字节继续从 lower view 继承，代码明确不先读取整个 base payload。FUSE 请求窗口有大小上限。[workspace/operations/file/write.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-workspace/src/operations/file/write.rs#L1-L20) [workspace/operations/file/write.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-workspace/src/operations/file/write.rs#L67-L85)
- Commit capture 当前 overlay，基于那一刻的分支快照构造候选文件系统根，并用一次 Save 写入对象及历史。若分支头已移动且没有 force，candidate 会合并到新 head；干净结果发布并成为新的 base。遇到冲突时不发布，但带冲突标记的合并结果会安装为 Workspace 本地 base，history binding 前移到新 head，供后续编辑/再提交。当前实现会在发布 lane 前先合并一次，并再次检查 head；若期间又前进，只在 lane 内合并新增变化，最多决定 1024 个 changed serial（inode 变更项），超出会以 LaneMerge 拒绝。[store/commit.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-store-service/src/store/commit.rs#L76-L86) [store/commit.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-store-service/src/store/commit.rs#L184-L206) [store/commit_merge.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-store-service/src/store/commit_merge.rs#L98-L124)

## 并发冲突、Branch Merge 与 Publish

- 分支头不同后，以绑定时的 root 为 base，逐路径比较 base、workspace/ours、head/theirs。若一侧未改，取另一侧；若双方文件都变了且内容在文本范围内，执行逐行三方合并。非重叠编辑可以合并；重叠片段写出 <<<<<<< / ======= / >>>>>>> 标记并记为冲突。超出文本界限或非文本内容记为 Binary conflict。删除/修改、类型变化也有单独冲突类别。故同一路径必然冲突不符合当前 core 代码；但文本判断有大小界限，其他变更组合仍可能冲突。[merge/decide.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/merge/decide.rs#L210-L270) [merge/text.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/merge/text.rs#L35-L83)
- 显式 branch.merge 有共同祖先时做 fast-forward；双方分叉时以 merge base 进行三方合并，干净结果成为一个双 parent Commit；若冲突，不创建结果分支。可选 merge unit 用路径 pattern 覆盖匹配路径及其后代；某 unit 两侧都改过时，以最近写入时间较新的一方整体胜出，平局取 ours，该 unit 内不再做行合并。[history/branch.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-store-service/src/history/branch.rs#L281-L334) [merge/units.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-content/src/merge/units.rs#L1-L13)
- branch.publish 直接把指定 branch Commit 作为 main 的下一层，语义是 overwrite-only，并报告覆盖的 main Layers；实现没有调用 merge。因此若 main 已有需要保留的新变化，应先 merge 分支再 publish。[history/branch.rs](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/core/crates/layerfs-store-service/src/history/branch.rs#L571-L615)
- 当前 JS SDK 的入口名是 workspace.commit；显式 merge 和 publish 则为 branch.merge、branch.publish。旧页面/旧版示例中的 sandbox.* 名称不应覆盖这份 main 源码快照的接口事实。[workspace/commit.js](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/sdk/js/src/workspace/commit.js#L1-L5) [branch/merge.js](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/sdk/js/src/branch/merge.js#L1-L7) [branch/publish.js](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/sdk/js/src/branch/publish.js#L1-L4)

## 查证边界

本记录是代码阅读所得，不是基准、运行验证或完整源码审计。Merkle-like 是对对象 ID + 子对象 ID 引用图的概括；实际块布局、保存时间及并发体验未在本次任务中实测。

## Silverwing 接入评估（Go 当前源码）

Silverwing 有有限的 workspace 抽象，但还没有可替换完整工作区生命周期的 provider。`WorkspaceProvisioner` 按策略分派创建与回滚，当前注册的是 Git worktree 和独立 Git clone；`ServiceWorkspace` 持久化 `RepositoryPath`、`WorktreePath`、分支和 Git 策略字段。[provisioner.go:77-92,109-131](../../internal/workspace/provisioner.go#L77) [models.go:282-302](../../internal/domain/models.go#L282) 状态读取器和工具启动器也各有小接口，但它们分别只抽象 Git 健康检查和“打开工具”，没有覆盖新后端所需的创建、挂载、清理、恢复、身份验证及快照提交语义。[git_health.go:104-120](../../internal/workspace/git_health.go#L104) [tool_launch.go:26-37](../../internal/workspace/tool_launch.go#L26)

工作区下游对真实 Git checkout 的依赖很深：生命周期按策略调用 `git worktree remove/add` 或删除受所有权标记保护的 clone；Git status、diff 校验真实目录是仓库根、分支和 common-dir；commit/push 直接执行 Git 命令；合并主分支会在该目录 fetch 并运行 `git merge`；Tag 交付以 `WorktreePath/.git` 或 Git common-dir 做资源锁，并可创建临时 detached worktree 来预检合并和推送。[lifecycle.go:203-222,229-286](../../internal/workspace/lifecycle.go#L203) [git_compare.go:209-260](../../internal/workspace/git_compare.go#L209) [git_operations.go:136-192](../../internal/workspace/git_operations.go#L136) [main_branch_merge.go:191-244](../../internal/workspace/main_branch_merge.go#L191) [delivery/tag_jobs.go:108-119](../../internal/delivery/tag_jobs.go#L108) [delivery/tag.go:802-850](../../internal/delivery/tag.go#L802)

工具执行也以宿主机文件路径为接口：Codex/Cursor launcher 收到任务目录字符串；IDE 和 terminal launch plan 将目标整理成绝对路径，按 Windows PowerShell、macOS 或 Linux 终端启动；通用进程 runner 把 `Directory` 赋给本机子进程的 cwd。内部 Codex app-server 集成也只是在本机进程上设置 cwd；README 明确 Silverwing 提供工具打开入口，“不会自动运行多个 Agent 会话”，因此不能把该 RPC 当成任务编码 Agent 托管层。[workspace_tools.go:22-59](../../internal/application/workspace_tools.go#L22) [system_tool_launch.go:60-116](../../internal/application/system_tool_launch.go#L60) [system_cli_terminal.go:11-47](../../internal/application/system_cli_terminal.go#L11) [runner.go:95-109](../../internal/process/runner.go#L95) [codex_rpc.go:18-46](../../internal/platform/codex_rpc.go#L18) [README.md:85-92,107-112](../../README.md#L85)

因此最小可行试点应把 LayerFS 限定为可选 Linux Agent 工作区，并先定义清楚：后端句柄/路径与宿主机可见路径的区别；provision、attach/mount、清理、恢复、健康检查、故障回滚和并发锁；以及 Agent 产生的 LayerFS 快照如何映射成 Git 分支/提交，供现有 status/diff/commit/push、主分支合并和 Tag 发布流程消费。只替换 `git worktree add` 不够，因为清理/恢复、Git 身份校验和后续所有操作仍把 `WorktreePath` 当作 Git checkout；而本机 Windows IDE、Codex、终端也无法自动理解仅在 Linux/WSL 环境中存在的路径。试点若暂时不改 Git 交付链，可在边界上把 LayerFS 内容导出/同步到普通 Git checkout，再复用既有 Git 流程。

Git linked worktree 和 LayerFS 的收益不同：Git 官方说明 linked worktree 与主仓库共享对象库及仓库级引用，独立的是工作目录、index 和 HEAD，并非每个都是完整 clone；LayerFS 的潜在收益是便宜的文件系统分叉、包含 ignored 文件的完整树和工具步骤快照。后者是否明显加速 Silverwing 要以本项目测量为准。此机 Go 模块与构建缓存位于用户级共享缓存，不能假定每个 worktree 都会重复下载 Go 依赖；若项目依赖大量本地 `node_modules`、编译缓存或生成文件，LayerFS 的 COW 收益更值得基准测试。[Git worktree 官方手册](https://git-scm.com/docs/git-worktree) 本轮 `go env GOMODCACHE GOCACHE` 输出分别为 `C:\Users\16776\go\pkg\mod` 和 `C:\Users\16776\AppData\Local\go-build`。

平台边界需精确描述：LayerFS 当前 main 提供 WSL2 桥接路径，所需 Docker Engine、FUSE 和 bundle 在 WSL Linux 发行版侧；它不是原生 Windows filesystem backend，Store 也不能放在 `/mnt/c`。因此 Windows 使用不是一概不支持，但 Silverwing 仍需适配后端启动、Linux/Windows 路径映射和工具访问。[LayerFS dsh README，当前 main:208-255](https://github.com/Ephemeral-AI-Lab/layerfs/blob/2e5061798d235819be45ef655e37ffa2799376d0/dsh/README.md#L208-L255)

本轮只静态阅读代表性 Go 源码与当前 README，没有安装、运行 LayerFS，也没有改动 Silverwing 代码或执行构建/测试。以上是接入边界初判，后端桥接与性能收益仍需原型验证。

迁移风险推论：若仅把一个现有 linked worktree 的工作目录导入 LayerFS，其 `.git` 指针仍可能解析到原 common Git directory（位于导入树之外）；若 Linux/WSL 环境看不到原位置，Git status、diff 与提交会失效，需保留/挂载该仓库元数据或改用显式导入导出流程。这是由 linked worktree 的共享仓库结构推导，未在 Silverwing/LayerFS 上实测。[Git worktree 官方手册](https://git-scm.com/docs/git-worktree)
