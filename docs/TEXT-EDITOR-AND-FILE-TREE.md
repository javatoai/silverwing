# TextEditor 与 FileTree（Go / MyGO）

本页适用于 silverwing-qa。组件使用功能名称，原生输入、绘制、字体回退、菜单和词法器来自现有 MyGO，不引入 WebView 或 JVM。

## TextEditor

`internal/desktop/text_editor.go` 是源码编辑公共入口。宿主持有 MyGO 的 `editor.State` / `Buffer`，组件应用共同的字体与语法主题，再调用原生编辑器；保存、草稿、文档身份、权限和冲突由宿主管理。已有 Buffer 不因成功保存或重新绘制而重建。

语言身份来自纯模块 `internal/filelanguage`：`Language` / `FromPath` / `SourceEditorOverride` 不依赖工作区、任务或 MyGO。公共编辑器的语言选择与 `internal/desktop/material_preview_language.go` 将中性语言映射为 MyGO 词法器；SDK 已支持的语言保留原优先级，补充词法器继续覆盖 Gradle、Properties、PowerShell 和 Batch。workspace 的旧语言类型与入口仅保留兼容 alias / wrapper。

| 项目 | 约定 |
| --- | --- |
| 源码字体 | 系统等宽，默认 13 DIP |
| 行高 | 默认 20 DIP；阅读缩放同比例变化 |
| 配色 | Mayoi 风格中性浅深底色、独立语法色，焦点与选区沿用应用蓝 |
| 装饰 | 当前行与行号、缩进引导线、圆角选区、失焦选区、查找与 Diff 标记 |
| 坐标 | 沿原生排版、UTF-8 字节位置与原有投影，兼顾 Tab、中文、Emoji、折叠 |
| 输入 | 原生光标、拖选、剪贴板、IME、命令与撤销保留在同一个 State |

JSON 工具保留格式化、错误定位、内联折叠、原文投影和会话历史。文本对比继续为一组双栏编辑器，中线拖动、纵向同步、横向独立及行内差异不变。工具单个输入上限仍为 **2 MiB**。

资料 TXT、SQL 和支持的代码文件直接进入公共编辑器。JSON / CSV 保留预览，通过“编辑源码”进入同一份草稿。

**Markdown 默认使用 MyGO 自带的 `markdown.View` 阅读器**：正文、列表、表格、普通代码块的排版与连续选取由框架提供，代码块使用 `DefaultCodeBlock`。应用只衔接原文复制、查找、图片权限、链接和现有 Mermaid 渲染。图表按阅读宽度等比缩放，点击可放大。

Markdown 仅提供阅读模式。任务资料、任务说明、AGENTS、Skills与生成预览的 Markdown 使用只读阅读器；正文保持 15 DIP 与 1.6 倍行高。资料的“查看 Markdown 原文”使用只读公共 TextEditor，保留精确复制与查找。正文、表格、代码块与 Mermaid 均不提供编辑入口，新建 Markdown 后也只进入阅读。应用层拒绝 Markdown 保存、草稿写入和手动丢弃；已有草稿保留，普通阅读不加载、恢复、删除或重写草稿缓存。新建、导入、重命名、删除和明确发起的外部同步仍由原文件管理业务负责。

Markdown 的 AST、源区间、表格、围栏、阅读投影及评论图片算法已移到 `internal/markdowntext`，只依赖标准库与 Goldmark。`internal/editor` 保留原 API 包装、运行时 part ID、自动保存和撤销等状态。domain 的需求评论处理直接使用纯算法；此次拆分不开放 Markdown 编辑、草稿写入或保存。

## 普通文本保存

资料文本的读取、编辑和最终编码大小上限为 **3 MiB（3 × 1024 × 1024 字节，含 BOM）**。这是资料文件边界；搜索单文件预算和 Mermaid 渲染各自已有的限制不因此扩张。

- 严格 UTF-8；UTF-16 及未知后缀探测出来的文本保留现有只读预览。
- 保留 UTF-8 BOM 与已有 CRLF / LF / CR，包括混合换行；新插入换行采用文件首个换行符，无换行时用 LF。
- 语法高亮和 JSON / CSV 预览只投影显示，保存始终使用原始源码。
- 复用已有草稿状态机与有界原子缓存，普通文本独立使用 `materials-text-drafts.json`；Markdown 缓存格式及路径继续保留。
- 草稿缓存兼容旧空缓存的 `records: []`，正常写入仍使用 Go 的对象映射；读取本身不重写缓存，非空未知数组及损坏格式保留并报错。Markdown 阅读不访问该缓存，损坏的旧草稿不会阻止阅读磁盘原文。
- 自动保存延迟 650ms，支持 Ctrl+S、恢复、失败重试和外部修改冲突操作。组合输入期间暂停落盘；切换、关标签和退出前回收宿主输入。
- 文件写入继续核对任务及资料身份、根目录、普通文件、符号链接 / junction 和基线摘要，再以同目录临时文件原子替换。冲突或失败保留草稿。
- 正常保存保持静默，错误操作区使用固定空间。

## FileTree

`internal/desktop/file_tree.go` 接受展示行、展开状态、选择集、当前打开文件及事件回调。原有构树、排序、文件操作和权限继续由任务资料与 Skills 宿主提供。

| 项目 | 约定 |
| --- | --- |
| 资料侧栏默认宽 | 248 DIP；已有保存宽度优先，保留拖动和收起 |
| 文件行 | 24 DIP，无额外行间隙 |
| 字体 / 图标 / 缩进 | 13 DIP 系统界面字体 / 14 DIP / 每级 14 DIP |
| 结构 | 轻顶部工具区、独立根目录行、连续文件列表、细分隔线 |
| 状态 | 展开、选中、当前打开分别维护；选中整行淡底，悬停更轻，失焦淡化 |
| 层级 | 细引导线，当前文件祖先路径强调 |
| 路径提示 | 500ms；滚动、拖拽和菜单期间抑制 |

资料宿主使用 `documents.MaterialsDescriptor`（应用层 alias 为 `MaterialsPermissions`）表达资料类型、读写根和写入/同步能力，FileTree 只消费展示行与宿主回调。描述符是已核对的 Snapshot 投影；重命名、删除、导入、保存和同步在后台重新检查任务、配置、资料身份及路径权限。

单击打开预览，双击固定；Ctrl / Shift 多选沿宿主维护。右键捕获菜单目标，不切换正文或触发保存，已经选中的多选组继续作为组操作。根目录操作在悬停或键盘焦点进入时显示，预留操作槽避免布局跳动。

资料保留拖入 / 导入、排序、刷新、空目录、滚动状态、重命名和删除的相关状态维护。Skills 保留三栏及窄屏切换，仅提供其实际拥有的操作。

## 开发验证

依赖方向由 `go test ./internal/architecture` 检查，包括其他操作系统和未启用 build tag 的非测试源码。门禁通过 fixture 验证违规 import，保护纯算法与公共语言模块边界；它不替代编辑交互、权限和保存行为验证。

按改动范围精确指定 Go 测试；公共编辑器的排版、拖选、剪贴板及换行测试位于本地 MyGO 模块，宿主绑定、草稿、文件树与只读阅读测试位于 `internal/desktop`、`internal/application` 和 `internal/documents`。

例如只验证 Markdown 阅读和普通文本编辑：

```shell
go test ./internal/desktop -run '^(TestMarkdownMaterialReader.*|TestMaterialTextEditor.*)$'
```

自动截图和文件操作验证使用独立 home、任务根与资料根；测试数据不指向真实用户资料。SDK 模拟缩放与真实显示器 DPI 分别记录，模拟输入事件不等同于实际输入法候选选择。
