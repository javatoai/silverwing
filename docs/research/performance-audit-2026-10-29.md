# Silverwing 研发版性能审查

日期：2026-10-29。本轮只读审查，使用六个 GPT-6.1-sol / max 子代理分域扫描，由主代理核对并串行运行隔离微基准。没有修改业务代码、使用真实账号或搬迁本地资料。

## 结论与优先顺序

确认有值得优化的重复工作，整理为下列 **25 个方向**。这里的“高/中/低”是预期收益，不是缺陷严重等级；静态成本可以确认，实际用户延迟和优化百分比仍须原生 profiling 或后续对照实验。

建议首批选择 **PF-01 JSON投影/坐标索引、PF-02资料目录投影、PF-11插件manifest单次解析、PF-22表单签名移出循环、PF-24远端refs定向查询**：边界清晰，容易独立验证。PF-03图片身份探测、PF-04列表估分、PF-05任务定点读取收益潜力也高，但先补账号/取消/权限/路径合同的计数与回归证据，再改变调度。PDF渲染、媒体传递、RPC会话和Git会话放在后续独立批次。

这些建议以保留现有 Meegle CLI 架构、资料权限、原子保存与任务身份校验为前提。一次性全局加缓存、增加无界并发或删除执行边界校验都不是可接受的性能优化。

## 扫描范围和版本

- 主对象：`codex/architecture-units` @ `a04de19fdf90fb6bdd06d273adf78210572d5c84` 加全部未提交内容；原工作区为 `C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing`。
- 对照：`release/snb-dev` @ `23fa44b0c64770fcec28181006cf6016b25bb6da` 加其既有WIP；原工作区为 `D:/workspace_ai/silverwing`。
- latest 全量静态索引覆盖 **1350 个生产 Go 文件**：693个第一方文件、657个本地MyGO替换文件。对照研发分支为1296个。文件清单、完整指纹与所有未跟踪源码均纳入冻结快照，QA/Product不在范围内。
- 深入追踪了桌面列表/设置/创建、编辑器与纯算法、资料树/搜索/监视、application/storage、Git/Tag/生产、CLI/需求/插件、媒体/PDF及实际引用的MyGO渲染路径。所有文件均纳入静态索引，但不等于逐个手工验证了框架未使用功能、生成shader、示例、所有平台或所有发布写路径。
- 下列25项在两份源码中均找到同类现状；没有将已存在的优化或仅旧版本额外重复排序列作latest问题。链接默认指向latest原工作区的核验行号。

## 已完成的隔离测量

Windows amd64，Go 1.27.1，Intel Core i7-11800H，基准默认CPU标记16；每项100ms目标、3个样本，表中为中位数。只使用内存/临时目录合成数据，操作系统文件缓存和机器当时负载会影响结果；没有优化后对照，不能推算“提升百分比”。

| 合成场景 | 每次时间 | 每次分配 | 分配次数 |
| --- | ---: | ---: | ---: |
| 未变JSON投影，64 KiB | 0.577 ms | 1.030 MiB | 24 |
| 未变JSON投影，1 MiB | 6.377 ms | 17.007 MiB | 35 |
| 未变JSON投影，2 MiB | 12.054 ms | 34.108 MiB | 38 |
| 资料树行投影，1000节点 | 0.915 ms | 0.342 MiB | 3015 |
| 资料树行投影，10000节点 | 9.205 ms | 5.099 MiB | 30023 |
| 折叠可见投影，10000节点但返回1项 | 1.403 ms | 1.151 MiB | 10007 |
| 快速打开无匹配，10000节点 | 5.034 ms | 0.231 MiB | 10000 |
| 扫描100个合成任务清单 | 18.065 ms | 2.722 MiB | 32006 |
| 扫描1000个合成任务清单 | 183.599 ms | 26.817 MiB | 319984 |
| 默认配置完整Load | 1.296 ms | 0.107 MiB | 1484 |

JSON fixture 是未变的 ASCII 输入、无折叠且未完成有效JSON校验，测的是同步投影分支，不包含解析或完整帧；有效但无折叠的输入也走同一 identityProjection 分支。资料fixture为一个顶层目录含全部子文件。treeRows返回全量行；collapsed-visible只返回1行但排序仍处理全树。它们均不是完整FileTree绘制/窗口帧时间。1000任务结果只含清单扫描，不含Snapshot的Skills/Tag历史和UI回写。

现有缓存参照：只读服务配置查询约1.74µs/次、导航判定约30ns/次，稳定阅读状态观察约51ns/次且0分配。它们证明已有缓存有实际效果，不能把“配置每帧反复JSON解码”或“阅读状态每帧落盘”列为问题；单服务查询基准也不能证明整个服务页没有二次查找。

## 按证据整理的优化方向

### PF-01 · JSON 原文未变仍重建全文投影，搜索与折叠另有重复坐标扫描

预期收益：高。证据：投影有隔离微基准；搜索/折叠为代码复杂度证据。

每次视图 build 调 syncJSONEditor，无折叠也创建全文 rune、positions 和字符串，之后才判断相等。2 MiB ASCII 未变输入中位 12.054 ms、34.11 MiB/op。查找开启后 K 个命中分别从文首做两次 rune→byte 转换，O(N×K)；折叠 gutter 已有缓存，但首次/失效重建对 E 个容器从文首转换 UTF-16，O(N×E)，不能称 gutter 每帧重建。

建议：按 source/parse/fold generation 缓存投影和匹配；一次线性建立 UTF-16/rune/LF-byte 边界映射，当前命中切换只更新活动索引。

边界：所有折叠入口、解析到达、格式化、撤销/恢复、外部输入必须失效；保留 CRLF、bare CR、Emoji 和隐藏命中的原文坐标。不能只比较显示字符串判定同一投影。

位置：[internal/desktop/shared_editor.go:51](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/shared_editor.go:51)、[internal/desktop/shared_editor.go:372](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/shared_editor.go:372)、[internal/desktop/json_tool_ui.go:174](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/json_tool_ui.go:174)、[internal/desktop/shared_editor.go:240](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/shared_editor.go:240)。

### PF-02 · 资料树与快速打开重复投影全目录，折叠没有省掉树构建

预期收益：高。证据：隔离微基准与实际调用链。

渲染前递归复制/排序全树，再展平并构造 FileTree 行，框架最后才隐藏折叠后代；MODIFIED_DESC 比较器还重复解析时间。10,000 节点 treeRows 中位9.205 ms、5.10 MiB/op；只返回1个折叠可见项也花1.403 ms、1.15 MiB。快速打开无匹配时同一 query 每次重扫，10,000节点5.034 ms/op。

建议：按文档 generation、任务/root、排序缓存排序树、扁平行、数量、路径索引及 lowercase 键；展开只更新可见行，query不变复用前200项结果。

边界：权限投影另绑定权限描述版本；刷新、重命名、身份/root/排序改变失效。保留原始路径拼写、quick DFS顺序、relative点特例与结果上限；后台安全检查继续执行。

位置：[internal/desktop/views.go:214](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/views.go:214)、[internal/desktop/materials_document_projection.go:94](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_document_projection.go:94)、[internal/desktop/materials_document_projection.go:178](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_document_projection.go:178)、[internal/desktop/materials_picker.go:247](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_picker.go:247)。

### PF-03 · 需求图片重复身份探测，评论图片还逐张串行加载

预期收益：高。证据：顶层 CLI 启动次数的静态计数，未测真实网络。

入口直接探测3个CLI；下载前后各强制3个，下载1个。没有重叠请求、正常成功的一张冷飞书图为10个CLI；同阅读器评论图片单个loading，20张为200次顺序顶层CLI。普通HTTP图则为9个身份CLI加1次HTTP。详情三个顺序强制刷新也会单独做前后探测，身份部分可达18个CLI。已有overlap singleflight不覆盖这些顺序或绕过session的探测。

建议：入口使用现有身份 session/probe，将同一次开始边界结果传给下游；先消除紧邻的重复前置探测，再评估小批量图片有界调度。

边界：保留下载后强制身份核对、配置/账号epoch、URL归属、generation和原子缓存写入。不得用长期全局TTL取代账号变化校验；计数不代表CLI内部HTTP总数。

位置：[internal/integrations/requirements.go:487](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/integrations/requirements.go:487)、[internal/integrations/requirement_cache.go:679](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/integrations/requirement_cache.go:679)、[internal/integrations/requirement_cache.go:699](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/integrations/requirement_cache.go:699)、[internal/integrations/requirements_attachments.go:17](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/integrations/requirements_attachments.go:17)、[internal/desktop/requirement_comment_images.go:42](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/requirement_comment_images.go:42)。

### PF-04 · 需求列表冷加载/强制刷新逐项串行读取节点估分

预期收益：高。证据：代码可确认 N 次顺序调用。

N个工作项各一页节点时，估分阶段额外启动N个workflow get-node CLI；100条就顺序等待100次，整表完成后返回。四类工作项查询也串行。已有14天整表cache、Sprint快照与同请求去重，主要影响冷加载和强制刷新。

建议：只读节点估分用2–4个worker有界并发，按原顺序汇总，保留逐项错误和进度；确有需要再加账号/项目/工作项/Sprint范围缓存。

边界：遵守平台限流、取消停止排队、账号和配置变更拒绝回写；强制刷新不得继续使用过期估分。并发收益与服务限制需假CLI及实环境分别验证。

位置：[internal/integrations/meegle.go:693](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/integrations/meegle.go:693)、[internal/integrations/meegle.go:581](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/integrations/meegle.go:581)、[internal/desktop/requirement_list_jobs.go:136](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/requirement_list_jobs.go:136)。

### PF-05 · 单个任务读取仍全扫任务根，全局 Snapshot 也过重

预期收益：高，随任务数增长。证据：任务清单扫描有隔离微基准；完整Snapshot未测。

taskInConfig为找一个TaskID调用Manifests.List→Scan，全目录Stat、ReadFile、schema/严格解码；资料读取和任务动作因此依赖总任务数。合成10/100/1000任务扫描中位1.701/18.065/183.599 ms，1000任务26.82 MiB/op。Snapshot另加载配置、Skills、所有任务及各任务Tag历史；进入每个设置子页按key后台读一次Snapshot，门禁有效，不能叫逐帧读盘。

建议：优先给已知TaskID提供安全的定点清单读取；全量Scan留给任务目录刷新。Snapshot按页面所需数据拆分或增量合并，完整刷新作为显式fallback。

边界：定点路径必须在TaskRoot第一层、校验TaskDirectoryName、schema、创建身份及链接/替换边界；执行时重读真实清单。不能把UI缓存作为授权或跳过归档/删除/迁移一致性。

位置：[internal/application/app.go:270](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/application/app.go:270)、[internal/storage/manifest_store.go:118](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/storage/manifest_store.go:118)、[internal/application/app.go:77](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/application/app.go:77)、[internal/desktop/settings_auto_load.go:69](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/settings_auto_load.go:69)。

### PF-06 · PDF 首次显示即全文文字索引，独占PDFium导致可见页等待

预期收益：高。证据：锁范围及可达调用链，需native profiling。

搜索面板关闭也自动request全文索引。TextIndex持有文档mutex和全局PDFium semaphore，直到所有页扫描结束；每字符最多两次native调用，累计上限500,000位置。若索引先拿锁，可见页渲染只能等全文。它是后台资源争用与首屏尾延迟风险，不是UI同步磁盘阻塞。

建议：按页提取文字，优先可见页；页间让出串行执行权，后台补全搜索，必要时首次打开查找才请求全文。

边界：每页native handle在释放锁前关闭；保留Close/取消/文档身份与搜索完整性状态，不能把未索引部分当作无匹配。

位置：[internal/desktop/native_media_pdf.go:74](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/native_media_pdf.go:74)、[internal/media/pdf.go:341](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/media/pdf.go:341)。

### PF-07 · PDF 快速resize/滚动产生大量最后不会展示的渲染请求

预期收益：高。证据：作业键、排队和完成门禁的代码证据。

请求按页+精确width去重，每个新键起goroutine；resize多个宽度、滚动离屏页的旧请求不会及时取消。完成时才按当前width拒绝发布，渲染/PNG编码解码已经执行。16个完成位图阈值不约束运行队列。

建议：每文档有界优先队列，同页仅保留最新width，优先可见页，移除失效待执行项。

边界：native调用开始后不可强行中断；继续保留完成时window/request/width检查，关闭等待后再释放PDF资源。

位置：[internal/desktop/native_media_pdf.go:200](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/native_media_pdf.go:200)、[internal/desktop/native_media_pdf.go:282](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/native_media_pdf.go:282)、[internal/desktop/native_media_pdf_jobs.go:32](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/native_media_pdf_jobs.go:32)。

### PF-08 · 服务页重复线性查找形成二次复杂度

预期收益：高，大服务组时。证据：静态复杂度；单服务配置bench不能覆盖全页。

同一build两次servicesForGroup，每个ServiceID线扫全部服务；过滤/绘制各线扫仓库，每行再线扫组配置判断启用，重排模式再查位置。S组服务、T总服务、R仓库时O(S×T+S×R+S²)。配置解析cache已存在，未消除这些查找。

建议：一次取得组服务，按Snapshot revision建service/repository索引，按config源建启用状态索引，空搜索直接走原列表。

边界：保留ServiceIDs原顺序、空列表fallback及重复身份返回nil的语义，不能用map最后一项静默覆盖重复配置。

位置：[internal/desktop/replica_pages.go:34](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/replica_pages.go:34)、[internal/desktop/replica_pages.go:203](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/replica_pages.go:203)、[internal/desktop/task_lookup.go:25](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/task_lookup.go:25)、[internal/desktop/action_form_payload.go:413](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/action_form_payload.go:413)。

### PF-09 · 任务/服务列表全量构建，任务索引和置顶身份还反复重算

预期收益：高，大列表时。证据：可达UI构建与复杂度证据，未测真实帧。

普通Scroll只裁剪绘制，仍构建/布局所有行。任务索引每build重建过滤slice/map与handles核对，匹配全量N任务时约5N次身份SHA/hex，置顶成员线扫还带O(N×P)。框架已有虚拟List可用。

建议：改用可见行List，完整数据投影按任务/组名/元数据/搜索/置顶/home revision缓存；身份只计算一次，handles在集合变化时reconcile，置顶slice附成员map。

边界：保留id+createdAt身份、置顶排序、键盘/右键目标、归档独立滚动和reveal。MyGO按需build且每帧最多3个build pass，不代表空闲60fps。

位置：[internal/desktop/task_list_ui.go:21](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/task_list_ui.go:21)、[internal/desktop/replica_pages.go:101](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/replica_pages.go:101)、[internal/desktop/task_navigation.go:144](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/task_navigation.go:144)、[internal/desktop/task_list_ui.go:108](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/task_list_ui.go:108)、[third_party/mygo/ui/list.go:368](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/third_party/mygo/ui/list.go:368)。

### PF-10 · Codex 每次 RPC 重启 app-server，一次面板/分页操作重复握手

预期收益：中高。证据：进程启动次数代码证据。

每个rpcRequest启动codex app-server并initialize，再发一个请求。任务面板snapshot有cwd时总includeSkills并forceReload，顺序启动两次；MCP检查config/read一次，P页再P次，合计P+1次。已有后台调度与stale门禁。

建议：一次snapshot/check内复用短生命周期RPC会话；Skills按标签首次进入或明确刷新加载。

边界：固定executable/home/cwd/代理配置身份，取消或身份变更关闭并等待清理；写RPC结果不明时不得自动重放。

位置：[internal/platform/codex_rpc.go:27](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/platform/codex_rpc.go:27)、[internal/platform/codex_ai.go:71](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/platform/codex_ai.go:71)、[internal/platform/mcp.go:341](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/platform/mcp.go:341)、[internal/desktop/codex_ai_jobs.go:38](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/codex_ai_jobs.go:38)。

### PF-11 · 插件 MCP 发现把同一 manifest 在内层循环反复读盘解码

预期收益：中高，改动较小。证据：代码可确认 O(P×M) 读取/解码。

先收集最多500个候选，再对每个配置插件遍历候选并在内层ReadFile/JSON解码。50插件×200候选可达10,000次读盘/解码；候选数量上限不提前终止WalkDir。

建议：在一次discover调用内，每份manifest仅解析一次，构建source+name索引，再按原mtime选择；优先做单次调用内去重，不必引入跨请求cache。

边界：保持来源、路径、大小、安全和现有版本选择规则。实际目录规模未知，未访问用户插件目录。

位置：[internal/platform/mcp.go:541](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/platform/mcp.go:541)、[internal/platform/mcp.go:558](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/platform/mcp.go:558)。

### PF-12 · 生产差异每切一个文件重复全量Git差异枚举

预期收益：中高。证据：稳定成功路径顶层 Git 进程静态计数。

每次切文件新建compareState、再次全量git diff --raw --find-renames、读blob/patch。普通修改文本每次12个Git进程，incoming模式13个，不含可执行文件发现；回访也重复。后台执行，主要增加切文件尾延迟。

建议：按repository/config/home/源SHA/目标SHA/incoming绑定不可变文件列表与有界预览session，复用已有workspace会话策略。

边界：保留前后目标/身份校验、列表归属检查、取消代次和stale；关闭、重新检查和目标变化释放，不跨仓库/账号共用。

位置：[internal/desktop/production_diff_dialog.go:68](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/production_diff_dialog.go:68)、[internal/production/diff.go:18](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/production/diff.go:18)、[internal/workspace/git_compare_trees.go:15](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/workspace/git_compare_trees.go:15)。

### PF-13 · Tag 单记录轮询放大全任务扫描及操作历史读取

预期收益：中高。证据：重复扫描/读取倍数代码证据。

每仓库独立tag.status，常见5/30秒调度。一次先扫全部任务，再为目标operation初读、Probe、最终读三轮完整当前任务历史。T任务、H历史、R仓库同轮且无共享flight命中时约R次任务扫描、3RH次历史读/解码，另有必要CAS。

建议：同轮使用一次authoritative候选快照供lanes分组，已捕获任务身份用安全定点读取，完成后刷新目标operation。

边界：保留每服务newer-release排序候选、CAS重读、账号/config/task scope，不可只信UI单条状态或取消探测前后校验。

位置：[internal/desktop/tag_genbu_poll_schedule.go:76](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/tag_genbu_poll_schedule.go:76)、[internal/application/tag_history_scope.go:35](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/application/tag_history_scope.go:35)、[internal/application/tag_job_probe.go:49](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/application/tag_job_probe.go:49)、[internal/delivery/tag_store.go:66](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/delivery/tag_store.go:66)。

### PF-14 · 生产构建轮询全读历史批次，但UI只消费单条增量

预期收益：中。证据：结果未消费与I/O链代码证据。

每个pending build按5/30秒refresh-build；已得report.Batch仍无条件Store.List全扫批次/解码/排序。桌面非production.list走mergeBatch，不消费report.Batches，历史越多每轮越重。

建议：完整Store.List仅用于显式列表/需要全历史的操作，refresh-build返回已持久化的单Batch；错误确认只定点Load。

边界：保持merge时间戳、删除门禁及持久化成功语义；不是每帧工作。

位置：[internal/desktop/production_jobs.go:671](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/production_jobs.go:671)、[internal/application/production_actions.go:153](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/application/production_actions.go:153)、[internal/production/store.go:67](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/production/store.go:67)、[internal/desktop/production_jobs.go:520](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/production_jobs.go:520)。

### PF-15 · Markdown 命中解析缓存前仍全文拼接并深拷贝 AST

预期收益：中。证据：可达热路径及分配代码证据。

普通阅读每build ensureEditor一次Source，Parts仅判空却深拷贝nodes，framework再Source拼接后才判断cache命中。O(N+AST节点)复制；Goldmark和MyGO parser缓存确实存在，不能误报成每帧parse。

建议：Document按内容version缓存Source，新增Empty/PartCount纯读取，避免为判空取深拷贝。

边界：NewDocument、外部同步、commit、restore/Undo/Redo需更新缓存；不能直接暴露可变parts以破坏封装。

位置：[internal/desktop/markdown_ui.go:82](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/markdown_ui.go:82)、[internal/desktop/markdown_ui.go:169](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/markdown_ui.go:169)、[internal/desktop/markdown_framework_ui.go:25](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/markdown_framework_ui.go:25)、[internal/editor/document.go:620](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/editor/document.go:620)。

### PF-16 · Markdown 超长代码块逐chunk重复计算文首偏移

预期收益：中高，长围栏时。证据：循环操作计数推导，未测耗时。

固定32行chunk，每个chunk从第0行累加offset，O(L²/32)。32,768行约16,760,832次前缀加法/次build；tokens与chunk高亮已有cache，问题是宿主偏移计算。

建议：外层累计offset使每行仅加一次，或按代码版本缓存chunk起点。

边界：保持OnText查找/复制与源码偏移合同；不需要更换Markdown阅读器。

位置：[third_party/mygo/ui/markdown/code.go:154](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/third_party/mygo/ui/markdown/code.go:154)、[internal/desktop/markdown_framework_ui.go:168](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/markdown_framework_ui.go:168)。

### PF-17 · 图片按节点/评论重复加载，缺少共享资源与累计并发预算

预期收益：中高。证据：按节点键/评论键及worker创建的代码证据。

Markdown构建全部block，全部图片节点可启动worker；同一路径/URL出现K次仍按nodeKey做K次加载解码，当前文档节点seen不等于可见范围淘汰。评论同URL跨commentID也重复下载/存K份。单图大小上限不能限制多图累计并发和像素内存。

建议：小容量图片作业池；按经验证的资源身份合并在途加载、共享不可变bitmap/字节，节点分别保存状态；可再做可见优先。

边界：key必须绑定资料/账号scope、来源和内容版本；保留每节点/评论URL归属与generation检查，不能仅按URL全局共用授权图片。

位置：[internal/desktop/markdown_preview_features.go:217](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/markdown_preview_features.go:217)、[internal/desktop/native_image_preview.go:37](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/native_image_preview.go:37)、[third_party/mygo/ui/markdown/view.go:251](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/third_party/mygo/ui/markdown/view.go:251)、[internal/integrations/requirement_comment_images.go:187](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/integrations/requirement_comment_images.go:187)。

### PF-18 · 进程内媒体预览 PNG 压缩→复制→解码往返

预期收益：中高。证据：编码/复制/解码链已确认，未测像素峰值。

PDF native bitmap先复制RGBA、编码PNG、复制到LRU和返回，再DecodeBitmap；图片也类似。4M像素一份RGBA约16MB，20M约80MB，未含编码/解码/GPU中间资源。稳定帧位图复用，并非每帧重做。

建议：保留PNG接口，另提供清晰所有权的通用RGBA结果供进程内UI消费，避免纯预览压缩往返。

边界：不得暴露即将释放的PDFium缓冲区，保留不可变cache和资源生命周期；真实峰值需fixture和native测量。

位置：[internal/media/pdf.go:247](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/media/pdf.go:247)、[internal/media/image.go:124](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/media/image.go:124)、[internal/desktop/native_media_pdf.go:230](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/native_media_pdf.go:230)、[internal/desktop/native_image_preview.go:83](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/native_image_preview.go:83)。

### PF-19 · 资料多选去后代 O(S×D)，一次树渲染计算两遍

预期收益：中高，大多选时。证据：代码复杂度与调用次数。

S个选中项对D个选中目录逐个filepath.Rel；1000个互不包含目录两遍约2×1000×999次非自身祖先判断。删除权限和计数各算一次。

建议：先在一次render复用结果，再按selection/root generation缓存，用祖先集合或trie减少父子比较。

边界：维持大小写/分隔边界与排序；操作去掉后代、复制路径保留完整选择的不同语义必须保留。

位置：[internal/desktop/materials_selection.go:77](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_selection.go:77)、[internal/desktop/views.go:219](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/views.go:219)、[internal/desktop/views.go:228](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/views.go:228)。

### PF-20 · macOS 文件watch全树rebuild发生在已有防抖之前

预期收益：中高，macOS批量文件事件时。证据：平台代码证据，未在macOS实测。

目录/祖先事件批次先WalkDir全树、逐祖先安全检查、reconcile vnode FD，然后才向200ms quiet/1s ceiling合并器通知。批量创建/rename可在一次UI刷新前重扫多遍。普通文件写已有touched局部分支，Windows使用ReadDirectoryChangesW，不是全树轮询。

建议：在native watcher内合并需rebuild的批次后安全捕获一次；可定位的普通文件变化保持局部分支。

边界：保留root/祖先替换、链接拒绝、FD关闭、取消唤醒；未知身份变化回退完整捕获，需macOS临时树计数验证。

位置：[internal/desktop/materials_directory_watch_darwin.go:99](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_directory_watch_darwin.go:99)、[internal/desktop/materials_directory_watch_darwin.go:129](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_directory_watch_darwin.go:129)、[internal/desktop/materials_directory_watch_darwin.go:286](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_directory_watch_darwin.go:286)、[internal/desktop/materials_live_refresh.go:121](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_live_refresh.go:121)。

### PF-21 · 提交历史读取无数量/输出边界，UI重绘全部提交行

预期收益：中，长历史时。证据：命令参数、输出缓冲与UI循环证据。

git log无max-count并读完整%B，baseline..HEAD虽限范围仍可能很大；stdout无预算。后台读取后历史dialog普通Scroll构建所有H行，每build O(H)，消息内存随全部字节B增长。

建议：绑定HEAD/baseline SHA分页+加载更多，虚拟列表，仅展开时读完整消息，限制输出预算。

边界：超限必须显示未完整/可继续读取，不能把截断当完整历史；scope/SHA变化重置分页和选择。

位置：[internal/workspace/git_history.go:25](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/workspace/git_history.go:25)、[internal/workspace/git.go:99](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/workspace/git.go:99)、[internal/desktop/workspace_history_dialog_ui.go:78](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/workspace_history_dialog_ui.go:78)。

### PF-22 · 创建/追加弹窗重复构造整个表单签名

预期收益：中，模块多时。证据：同一build循环与签名完整计算证据。

preview存在时当前服务每个模块都重新遍历全部选中模块、Marshal并SHA256表单。当前A模块、全部M模块为O(A×M)，单服务可O(M²)，折叠模块仍执行。主UI preflight观察另重复表单/完整配置指纹，追加即使pending/无预检也构造请求。

建议：本次表单绘制后统一检查一次；追加只在有有效预检且非pending时做绘制期签名。跨帧复用再使用完整revision。

边界：所有字段、顺序、模块、说明、需求及配置编辑都失效；执行前/结果回写仍核对完整签名、身份、取消与stale。

位置：[internal/desktop/task_creation_ui.go:1930](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/task_creation_ui.go:1930)、[internal/desktop/task_creation_ui.go:696](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/task_creation_ui.go:696)、[internal/desktop/task_creation_preflight.go:205](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/task_creation_preflight.go:205)、[internal/desktop/task_append_ui.go:646](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/task_append_ui.go:646)。

### PF-23 · 全文搜索逐文件投递进度，主线程可能积压数千回调

预期收益：中低。证据：回调上限及FIFO路径的代码计数。

最多5000候选，每个候选一次progress，加开始结束约5002次。每次win.Update入主线程FIFO。后台磁盘搜索不阻塞UI，但进度合并前有大量闭包与回调；不能把回调数等同帧数。

建议：只保留最新progress，以一个pending UI callback或50–100ms节流合并，终态立即投递。

边界：保留generation/任务/关闭门禁，取消后不回写，最终值与错误不能丢。

位置：[internal/documents/fulltext.go:237](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/documents/fulltext.go:237)、[internal/desktop/materials_picker.go:174](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/materials_picker.go:174)、[third_party/mygo/content.go:172](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/third_party/mygo/content.go:172)。

### PF-24 · 资料 Git 同步三次拉全远端refs，仅查一条分支

预期收益：中低，改动较小。证据：常规已有仓库成功路径的命令计数。

remoteBranch执行未过滤refs的ls-remote再找一个refs/heads/branch，常规同步阶段调用3次。远端A个tag/branch时每次输出/解析O(A)，无该命令专属输出预算。

建议：保留各阶段确认，仅把远端查询限制为已验证目标分支，增加有界输出。

边界：区分不存在分支与命令失败；保留mutation前scope/分支检查、材料锁、temp及范围外保护，不以性能理由移除检查。

位置：[internal/workspace/materials_git_sync.go:478](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/workspace/materials_git_sync.go:478)、[internal/workspace/materials_git_sync.go:223](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/workspace/materials_git_sync.go:223)、[internal/workspace/materials_git_sync.go:417](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/workspace/materials_git_sync.go:417)。

### PF-25 · Mermaid 冷缓存安装资源仍在 UI 调用链同步执行

预期收益：低，首次使用时。证据：同步I/O链；未测冷启动。

renderMermaidDiagram同步load renderer；未安装资源时Mkdir/Stat、读最多24MiB资源、写临时文件并rename，然后才异步渲染。安装后直接返回，不能称每帧复制。

建议：把版本/平台资源初始化安装也放入异步加载状态，复用初始化结果。

边界：保留合法平台/版本目录、原子替换、安装失败反馈和关闭门禁；现有渲染防抖/双槽/超时/cache不应删除。

位置：[internal/desktop/markdown_ui.go:1092](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/desktop/markdown_ui.go:1092)、[internal/editor/mermaid.go:182](C:/Users/16776/.codex/worktrees/snb-release-fixes/silverwing/internal/editor/mermaid.go:182)。

## 已有优化，不应重复建设

- 普通文本3 MiB含BOM、工具输入2 MiB上限；Buffer按版本观察，成功保存不重建，文本高亮和行布局有缓存/后台工作。Markdown默认只读，未把遗留编辑路径当当前热路径。
- 配置读取已有UI源字符串cache，导航读取按key防重且异步。文本草稿持久化250ms防抖、实际文件保存650ms防抖；原子写入/摘要与stale门禁承担正确性，不建议去掉。
- 资料磁盘加载有requested门禁、文件job异步且拒绝过期结果；Windows watcher不是定时全树扫描。全文搜索180ms输入防抖已有，但不等于进度回调节流。
- workspace diff已有16文件/32 MiB LRU、4文件batch预取、前台优先和单文件大小限制；workspace状态有物理路径去重、最多4worker、32任务展示cache。生产diff未复用它是另一条具体路径。
- 需求已有整表cache、正文共享请求、正文/图片并发槽、身份singleflight、账户代次与CLI退避。问题是有些调用绕过或顺序重复这些机制，不能泛称没有cache。
- PDF使用虚拟List；PDF页上限4M像素、图片20M像素，PNG LRU32 MiB。完成位图和GPU纹理有复用/淘汰；字体、layout、atlas有预算。未发现可支持“每帧重新创建字体/上传所有纹理”的证据。

## 验证边界与证据

本轮没有运行真实GUI CPU/heap/锁/帧时延profile，没有执行真实Git/Meegle/Codex网络请求、macOS原生场景或用户数据压力测试，也没有实施优化。函数级合成测量不能替代真实用户数据的端到端性能。

没有重跑完整回归、构建、race或默认vet；基准使用 `-run ^$ -vet=off`。这不会消除既有unsafe.Pointer告警，也不能证明优化后的行为正确。

证据目录：`build/performance-audit-20261029/`，含snapshot.json、两版文件指纹和status、static-index.json、measurement.json精确命令、benchmark-summary.json、desktop-bench.log、storage-bench.log、findings.json和final-source-audit.json。两个benchmark-only文件只写在source/latest隔离副本内；原两版纳入快照文件共4461份在测量后重核，未发生漂移。

本报告新增在docs/research中；未提交、推送或发布。后续每个优化应先固定指标和fixture，再验证功能合同、失效与内存上限，按单元分批实施。
