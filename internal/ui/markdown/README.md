# 公共 Markdown 组件

原生 UI 统一使用 `silverwing-go/internal/ui/markdown`（包名 `markdownui`）。组件不依赖主应用对象、任务、Sprint 或具体后端。

独立示例：`go run ./examples/markdown-components`。

```go
reader := markdownui.NewReaderState(markdownui.Document{
    ID: "help:document-id", Source: text, Path: sourcePath,
})
editor := markdownui.NewEditorState(markdownui.Document{
    ID: "materials:document-id", Source: text, Path: sourcePath,
})
markdownui.MarkdownReader(c, reader, markdownui.ReaderOptions{})
markdownui.MarkdownEditor(c, editor, markdownui.EditorOptions{
    OnChange: func(snapshot markdownui.Snapshot) { /* 更新宿主草稿 */ },
    OnSave:   func(snapshot markdownui.Snapshot) { /* 宿主保存该版本 */ },
})
```

- 状态在 UI 线程创建并跨帧保留；`Document.ID` 同时区分文档及所属页面／任务上下文。
- `Path` 只用于解析链接，不授予文件访问能力。`OnNavigate(LinkTarget)` 交给宿主处理明确的打开请求；宿主负责权限和过期身份校验。
- 阅读器只读，支持查找、复制、缩放及右键查看源码。`SetDocument` 更新内容；切换身份时重置交互状态，并取消旧资源上下文。
- 编辑器默认原位实时编辑。右键切换实时编辑、只读预览、源码；所有编辑模式共用一个 Buffer、选区及撤销历史。
- 编辑器的实时编辑和源码模式都查找当前 Buffer 的原始内容，包括链接地址和标记；结果在同一原生编辑面定位，源码块同时高亮。只读预览按渲染正文查找，输入查询不会把焦点移回正文。
- `EditorState.RevealLocation(line, column, fragment)` 在当前 Buffer 中定位，行列从 1 开始；行号为 0 时按 MyGO 标题锚点定位（同名标题取首个）。定位保留模式、原文字节与撤销历史；未找到标题或处于输入法组合时返回错误。
- `OnChange` 只发布已提交输入；`OnCompositionChanged` 连接宿主输入法／自动保存策略；`OnSave` 是请求，不自行写文件。
- 保存成功回执不调用 `Reload`。仅明确接受的磁盘重读、草稿恢复或丢弃操作调用 `Reload(source, expectedVersion)`；过期版本和输入法组合期间的重载被拒绝。
- 页面销毁时调用 `Close`。异步资源适配器捕获 `Context()` 与身份／版本，在交付时重新验证；关闭或切换文档后不能回写旧结果。

## 扩展与组合

`ReaderOptions.Theme` 接入主题；`Native` 接入 MyGO 图片、Mermaid、复制原文及来源投影等扩展。`CopyNativeOptions` 只复制公开配置，不复制资源缓存。

`ContentOnly` 将正文嵌入宿主已有的滚动／联合查找区域，保留文本映射和菜单及正文自身高度。普通页面直接使用完整组件，在 Row 中会填满可用高度。相关文档可传同一个 `FindController` 联合查找，文档 ID 应唯一；切换匹配时由目标文档滚动定位。

实时编辑中链接单击打开文字／地址卡片，Ctrl/Cmd+单击请求导航；只读阅读单击请求导航。引用式链接编辑只影响当前位置；移除链接保留标签的行内文字和格式，避免将 `##` 等文字重新解释为块级语法。切换预览和编辑保留选区，继续使用查找的上一个／下一个按钮才主动定位。

表格在网格中直接编辑，Tab/Shift+Tab 切格，末格 Tab 新增行，Enter 移动，Shift+Enter 插入格内换行；右键增删行列及调整对齐。

方向键先在单元格的真实视觉行内移动；到首／末行后，上下进入同列的相邻行，不受其他单元格撑高行高影响。正文左右边界可用左右键进入同一行的相邻单元格，鼠标点击高单元格的空白处仍定位在该格。导航本身不改写 Markdown，修饰键选择和输入法沿用原生流程。

原文省略尾部空单元格时，方向键仍保留目标列及真实光标位置；只在明确输入时同事务补齐所需分隔符，可一次撤销回原文并继续编辑同一列。

左／中／右对齐的正文、输入法预编辑、空格以及光标／选区／鼠标命中使用一致的文本形状参数，避免内容在右边而光标或高亮仍落在左边。

实时编辑默认启用列表／引用写作键：有序列表 Enter 续写实际编号，无序列表保持标记，待办列表的新项恢复为未勾选；空项 Enter/Backspace 逐层退出。Tab/Shift+Tab 调整列表项及其连续正文、子项的层级；第一项没有同级前驱时不盲目缩进为代码。Shift+Enter 在当前列表项内续行，不新增编号。引用段落回车保留引用前缀，空引用逐层退出。一次操作使用同一 Buffer 的一次撤销，保留其他内容和原始换行；表格、代码、HTML、frontmatter 以及显式源码模式保留原有键盘行为。底层为 SDK 默认关闭的 `EditorOptions.ContinueBlocks`，公共组件默认开启。

公共编辑器同时启用 SDK 默认关闭的 `InteractiveTasks`：单击 AST 确认的待办方括号标记切换状态，仅替换原文中的一个字符，保留当前选区并可一次撤销。拖选、Shift 单击、右键、源码模式和只读不触发切换，输入法组合时不同时勾选；纯阅读器继续只读展示。

缩进有序项时若现有编号不能直接中断前项正文，会同事务补必要的分隔空行以形成合法嵌套，不重排其他项的编号。反缩进保留合法分隔空行；撤销会恢复操作前原文。

普通代码块始终保留语法高亮卡片，点击正文直接使用整个文档的原生 Buffer、选区、输入法及撤销历史；输入中文预编辑也在卡片内显示。右上角的原生语言下拉支持 SQL、Python、JavaScript、TypeScript、Java、Go、JSON、YAML、Shell、HTML、CSS 和纯文本；未知语言保留原值。语言选择只修改 Markdown info 的语言 token，保留 metadata、其他空白和原文换行；同一语言的别名选择不产生修改。缩进代码选择语言时将当前块转换为带语言围栏，转换可一次撤销。正文边缘删除保留隐藏围栏，粘贴闭合围栏会在同一事务内延长外层围栏。

此行为由 MyGO `EditorOptions.InPlaceCode` 提供（SDK 默认为关闭，公共 `MarkdownEditor` 默认开启）。`CodeBlockView.LanguageSelect`、`Body` 和 `CopyButton` 可供自定义 `CodeBlock` 包裹组件复用；已有 `Options.Code` 语言专属渲染继续保留原有预览／激活源码契约。语言控制修改代码块类型，不改变文件名，不执行代码；只读和预览不接受修改，源码模式照常显示完整原文。

相邻的顶层闭合围栏代码卡片之间保留一行可定位的输入区，上下方向键也可进入。已有空行直接复用；原文没有空行时先显示虚拟输入行，布局、点击和聚焦都不改源码。只有明确输入、粘贴或提交输入法时，才在同一个 Buffer 事务中补所需换行并写入代码围栏之外，可一次撤销，保留原始换行和围栏 metadata。只读时仅保留视觉间距，源码模式保持字面编辑。

文末的顶层闭合代码卡片后也有可直接落笔的空行，复用已有尾部空白；没有末尾换行时，首次输入才补必要的原格式换行。空输入及取消预编辑不写源码。新增一个闭合代码块后会重新生成文末输入区，未闭合代码仍在代码正文中续写。语言选择器使用与卡片融合的默认背景、轻悬停提示及原生键盘焦点和下拉菜单。

`ResolveLink` 支持 HTTP(S)、mailto、相对路径、Windows 盘符、file URI、行列后缀及锚点，拒绝脚本和网络文件协议。文件读取、系统打开、保存及目录权限留在宿主适配器；独立组件测试无需启动 Silverwing 或提供后端。

## 日常写作命令

`EditorState.Execute(EditorCommand)` 和 `CommandState` 统一文字样式、正文／标题／列表／引用、代码／表格／链接／图片插入及整块删除。Ctrl+B、Ctrl+I、Ctrl+Shift+X、Ctrl+反引号与选区浮动条、右键复用命令；Ctrl+K 打开链接插入卡片。工具条仅在有效可见选区时出现，保留编辑焦点，不增加常驻模式栏。

公共组件启用 SDK 默认关闭的 `SmartEditing` 和 `ConfirmCodeFenceOnEnter`。新输入的三个或更多反引号及可选语言先保留文字，Enter 才生成完整围栏并进入正文；Undo 恢复草稿，已有文件／完整 Markdown 粘贴仍标准解析。草稿 policy 不修改保存源码，也不吞并后续块。代码 Tab 使用四个可见空格，多行缩进／反缩进保留内容，Enter 继承行内缩进，空代码块 Backspace 退出。配对、选区包裹、跳过自动闭合及空对删除不接管 IME 组合。

Ctrl+H 提供当前 Buffer 原文替换，ReplaceCurrent／ReplaceAll 不访问共享查找中的其他文档，全部替换一笔撤销。代码／表格／源码粘贴保留字面／TSV 行为；普通正文通过可选 ReadClipboard 接收文字、HTML、图片或文件，Ctrl+Shift+V 强制纯文本。

OnImportResources(ctx, request, complete) 由宿主异步复制资源，回调须在 UI 线程交付。request 捕获文档身份／路径／版本／选区，过期、关闭、只读或预览结果不写正文。没有资源回调时仍可插入图片网址。资料宿主沿用文件服务与 scope 校验，复制至文档基名.assets 并插入相对引用；撤销不删除磁盘资源。

GetViewState／RestoreViewState 提供内容摘要约束的选区、模式与视口状态；宿主持有按文档身份隔离的缓存。恢复不修改源码／历史、不抢焦点，显式定位／查找和新用户操作优先。阅读缓存可选 editor 字段不属于文档或用户配置。

SDK 的 `ApplyEdits`／`ReplaceRange` 始终表示原文编辑；已编码的富文本、链接及资源引用使用 `InsertMarkdown`，由组件补虚拟输入行的分隔和表格内换行。文字格式保留未选片段的原始换行与嵌套样式；跨位连续编号同时保持子项缩进归属。资源相对引用按 URI 编码，文档名或图片名中的 `#`、`%`、中文及空格保持对应真实文件。

异步插入和编辑弹框用 `CaptureInsertionPoint`／`RestoreInsertionPoint`／`InsertMarkdownAt` 保存短期插入意图，除版本和选区方向外还保留虚拟表格列及写作行；位置不可跨 Buffer 或模式使用。HTML 标题、列表和表格片段在正文中插入时补块分隔。有序 HTML 列表按实际标记宽度缩进子项；无法无损表示为单个 GFM 代码跨度的表格内奇数反斜杠加管道符组合，保留为纯文本，避免丢字符。

批量文本、粘贴和 IME 提交的边缘空白保留在新格式标记之外，源码模式保持字面输入并单独结束输入撤销组。部分选中另一种已有样式时按源内联边界平衡拆分，优先选用可解析的标准标记；有歧义的相邻标记必要时以标准空注释分隔。转换前核对文字、其他样式和链接目标，无法安全转换时保持正文不变。公共命令会记录用户操作，迟到的阅读缓存不会关闭刚打开的插入框或清除后续样式。本地选图保留面板填写的图片说明，空说明才使用文件名。

多行粘贴结束待输入格式，保留完整 Markdown 块；已有样式在插入边界结束／接续，操作和待输入状态可一起撤销。选区样式批次在完整文档的引用定义上下文中再校验一次，跨越链接边界且不能安全表示时拒绝转换。生成链接、资源路径及入站 HTML 时保护字面实体，文件名中的 `&copy;` 不会被变成另一个字符。滚轮及滚动条按下都优先于迟到的位置恢复，程序设置滚动位置不算用户按下滚动条。

行内代码也执行同一文字／其他样式保真检查。给选区加链接时复用已编码的源码，显示文字卡片展示解码后的文字；用户未改文字时保留原有样式、实体和转义，只补不平衡的标签方括号，跨隐藏标记不能安全表示时拒绝。滚动条交互覆盖未单独 TrackScroll 的嵌套表格横向容器。

行内代码原位输入反引号或边缘空格时，同事务调整必要围栏和填充，保留原有较宽围栏；不支持的多段内容会保留原文并提示使用代码块。图片面板的默认说明始终按可见文字传给宿主。粘贴与当前相同的完整行内格式时合并冗余包裹，不能安全组合的混合格式保留原文并报告错误。

## 文档属性

Reader 和 Editor 自动将完整、有效的文首 YAML 映射展示为公共 `MarkdownProperties`：标签按层级路径换行，日期用中文名称，其他属性在“更多属性”内查看与复制，整个区域可折叠。独立宿主也可使用 `NewPropertiesState(Document)` 和 `PropertiesOptions` 组合此组件。读取组件和只读选项不暴露编辑入口；Source 模式始终保留完整原文。

编辑器通过 `ApplyTagChange(TagChange)` 统一处理新增、改名和删除，校验文档身份、版本、只读及 IME。弹框 Enter 保存、Esc 取消；提交为原 Buffer 中一次可撤销的局部事务，不更新日期，不重新序列化 YAML。原字段、注释、顺序、BOM/CRLF 与正文保持。属性动作保留视口并恢复正文输入焦点。

支持字符串、null、空/缺省 tags、常规 block 列表和单行 flow 列表。标签含 alias/anchor、显式复杂 tag、非字符串或多行 scalar 等不能安全修改的形式提供源码入口；无关复杂字段保留。无效/未闭合 YAML 仍按原文显示。源码查找或显式位置命中属性时切源码定位；普通正文首位 Backspace 不删除隐藏的闭围栏。
