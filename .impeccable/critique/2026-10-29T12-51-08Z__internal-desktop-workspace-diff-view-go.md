---
target: Silverwing Git diff 查看器
total_score: 26
max_score: 40
na_heuristics:
p0_count: 0
p1_count: 0
target_identity: "file:D:\\workspace_ai\\silverwing\\internal\\desktop\\workspace_diff_view.go"
target_fingerprint: "sha256:deef0ec33b5df1aa8f59445e6f8006d467fcaf2f3928cafddf42041cf56117df"
target_path: "D:\\workspace_ai\\silverwing\\internal\\desktop\\workspace_diff_view.go"
timestamp: 2026-10-29T12-51-08Z
slug: internal-desktop-workspace-diff-view-go
---
# Silverwing Git diff 查看器优化评估

日期：2026-10-29（Asia/Shanghai）。范围：工作区 Git 审阅页、原生 diff 与逐行回退渲染。版本：release/snb-dev，HEAD 23fa44b0c64770fcec28181006cf6016b25bb6da。此次没有修改应用代码。

Method: dual-agent (A: /root/diff_design_sol_max · B: /root/diff_quality_sol_max)。两名子代理均使用 GPT-6.1 Sol、max 推理；A 完成后，主代理才读取 B 的 detector 和技术报告。此前两名代理已停止，未纳入最终评估。

结论：沿用现有左右对比、语法与行内高亮、分栏及逐文件阅读位置恢复，优先补齐操作一致性、上下文阅读和文件定位。

## 证据和验证边界

检查了当前源码与仓库现有 docs/assets/readme-git-review.png，并对照当前组件。截图是已有示例，不能代表本次运行窗口。没有操作用户原生窗口，实际系统键盘焦点、DPI、辅助功能和 GPU 呈现未验证。

56 个桌面/editor 顶层目标测试通过：桌面 22 项，editor 34 项；主代理另运行 3 组后端性能证据测试，全部通过。没有新增测试或运行全仓库测试。

detector 返回空数组，但默认设计规则不覆盖 Go/MyGO，此结果不构成原生界面质量通过。以下结论依据源码和限定测试。

## 已有优势

- 来源标题、行号、红绿背景、加减号及字内强调形成清楚的代码阅读顺序。
- 两种渲染器均已有同步垂直滚动、独立水平滚动、首次定位改动、拖动分栏和切换文件后恢复位置；现有离屏测试覆盖了这些行为。
- 原生 editor 已有 Ctrl+F、Ctrl+G、F3、Alt+F5 / Shift+Alt+F5、Alt+Z。rows 已有视区虚拟化；后端已有快照、会话缓存和预取。这些应作为后续优化的基础。

## 优先改进

### 1. [P2] 统一查找、跳行和差异导航

普通原生路径已有这些能力；CRLF、两侧合计超过 256 KiB 或 8000 行、以及非新增文件的“仅看改动”会改走 rows。rows 只有逐行可选文字和增删统计，没有同等的 find、GoToLine 和 hunk 操作。

建议把查找、转到原始行号、上一处/下一处和块计数定义为共同能力，并给原生已有命令增加可见入口与快捷键提示。保持文件内容、原始行号和比较语义。

另一个静态风险：顶部块计数只由初始化和按钮更新，原生键盘导航使用 SDK 自身状态。查找、点击或快捷键跳到别处后，计数及下一次按钮目标可能仍依据旧 index。可使用 SDK CurrentHunk 同步实际位置和回绕策略；此错位尚未在真实窗口复现。

证据：[回退选择](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_view.go:174)、[输入边界](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_view.go:280)、[rows 内容](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_rows_view.go:284)、[块计数](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_view.go:307)、[SDK CurrentHunk](D:/workspace_ai/silverwing/third_party/mygo/ui/editor/diffview.go:418)。

### 2. [P2] 增加“改动及附近 3 行”的阅读方式

现在主要在完整文件和严格改动行之间切换。前者需要长距离滚动，后者移除了函数、条件和注释等上下文。SDK 虽有折叠分隔条，但工作区开启 ChangedLinesOnly 后先离开 native；能够进入 native 时 collapse 实际为 false，因此没有可进入的上下文折叠模式。

建议增加独立的“折叠未改内容，保留 3 行上下文”，按块向上/向下展开或展开全部；保留严格“仅看改动”的现有语义。不同投影之间用原始行号或块锚点映射阅读位置。

证据：[严格过滤](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_view.go:174)、[native 调用](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_view.go:370)、[已有展开组件](D:/workspace_ai/silverwing/internal/desktop/diff_native_view.go:119)。

### 3. [P2] 完善文件路径显示和筛选

“按相对路径平铺”仍通过同一行组件只显示 basename。api/service.go 和 worker/service.go 同时变化时都显示 service.go，只能悬停或选中后确认目录。列表也没有路径筛选。

建议显示文件名与次要父目录，增加按文件名/相对路径筛选和匹配数量，保留目录树与完整路径 tooltip。可用 A/M/D/R 标记增强文件变更类型辨认。

证据：[平铺分支](D:/workspace_ai/silverwing/internal/desktop/workspace_review_page.go:476)、[统一 basename](D:/workspace_ai/silverwing/internal/desktop/workspace_review_page.go:543)。

### 4. [P2] 持续显示比较范围

“包含本地未提交”和“模拟应用到主分支”在更多菜单，关闭后状态不够常驻；主分支引用和基准 SHA 主要在 tooltip。提交范围模式已有短 SHA 和次数，不属于完全缺少范围信息。

建议模式旁显示简短摘要，如“origin/master 的共同祖先 → 当前提交 · 含本地未提交”；模拟结果明确标注“模拟应用到主分支”。完整 SHA 继续放 tooltip，不改变现有比较算法。

证据：[菜单状态](D:/workspace_ai/silverwing/internal/desktop/workspace_review_page.go:134)、[来源标题](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_view.go:376)、[详细 tooltip](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_view.go:434)。

### 5. [P2] 性能优先测首次准备和较大输入

本机既有 fixture 为 160 个文件、8 个变更，覆盖每文件 896 B、8 KiB、256 KiB。首次准备和列表约 2.1–2.8 秒，冷预览约 433–866 毫秒，缓存读取约 3–8 毫秒，预取后选择约 3.8–6 毫秒。这是后端服务层耗时，不是原生窗口首帧或持续帧率。

rows 已按视区构建，但初次仍对全部来源分词、生成 spans、测量每行宽度；统计每次绘制仍扫描 rows。文件列表当前对全部文件构建，树和排序结果也会重新生成。

建议利用已有 prepare/blob_read/diff/parse/first_paint 阶段日志定位瓶颈，缓存不变的文件树、排序和统计，并对文件列表按视区构建。单独测 rows preparation 和首帧后再决定分片或后台计算，同时保持跨行词法状态及身份/取消边界。

证据：[rows 准备](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_rows_view.go:60)、[统计扫描](D:/workspace_ai/silverwing/internal/desktop/workspace_diff_rows_view.go:269)、[文件列表](D:/workspace_ai/silverwing/internal/desktop/workspace_review_page.go:473)、[性能样本](D:/workspace_ai/silverwing/internal/workspace/git_compare_session_perf_test.go:46)。真实 UI 卡顿改善幅度待测。

## 次要机会

原生 Alt+Z 已有效：Wrap=false 是默认值，editor 会通过 toggledWrap 覆盖，不能误报为禁用。可增加可见自动换行入口并保存偏好；rows 的固定 24px 虚拟行高需要同步调整。还可改进行内高亮精度，并在预览超限或非文本原因旁增加显式的“在编辑器中打开”入口。

现有查看器只读。后续缓存继续只保存轻量阅读状态，避免长期持有历史全文。

## 设计健康度（静态暂定）

| Nielsen 项目 | 分数 | 依据 |
|---|---:|---|
| 系统状态可见 | 3 | 有加载、选中、统计；范围状态不够常驻 |
| 贴近用户语言 | 3 | Git 语言熟悉；整体差异边界可更明白 |
| 用户控制与自由 | 3 | 返回、收起、分栏和阅读恢复已具备 |
| 一致性与标准 | 2 | native 和 rows 能力不同 |
| 错误预防 | 3 | 只读且请求绑定快照；范围可见性仍影响判断 |
| 识别而非记忆 | 2 | 平铺同名、范围和快捷键需额外发现 |
| 灵活性与效率 | 3 | 原生快捷键丰富；回退和文件定位不足 |
| 简洁与视觉层次 | 3 | 内容优先、分组清楚 |
| 错误诊断与恢复 | 2 | 有原因和刷新；受限文件恢复入口可增强 |
| 帮助与说明 | 2 | 有 tooltip；快捷键提示不足 |
| 合计 | 26/40 | 静态评分，不能替代真实窗口验证 |

界面的代码双栏、文件树和紧凑控制适合研发工作台。认知负担主要来自范围状态需要记忆和无上下文阅读需要切换；顶层只有三个比较模式，没有证据支持“顶层选项过多”。高频审阅者最受回退操作断层、同名文件和上下文缺失影响；依赖非颜色信息的使用者可从明确的变更类型标记获益，代码行已有加减号。

## 验证记录

后端命令：

    go test ./internal/workspace -run '^TestGitCompare(SessionReuse|SessionQuarterMiB|Prefetch)PerformanceEvidence$' -count=1 -v

PASS，包耗时 27.010 秒。fixture 使用临时仓库。

桌面/editor 的命令、56 项测试范围和原始日志见 [独立技术证据](C:/Users/16776/.codex/visualizations/2026/10/29/01a187a9-1bbd-7bd0-b366-0246cc7f1f5c/diff-quality-sol-max.md)。

建议第一轮先处理第 1–3 项。第 4 项是较小的状态呈现改进，第 5 项以阶段日志和独立测量决定具体改动。
