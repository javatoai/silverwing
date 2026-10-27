# 架构与职责边界

Silverwing 是基于 Go 与 MyGO 的原生桌面工作台。桌面界面在进程内调用应用层，本地功能不依赖 HTTP 服务、WebView 或 JVM。Git、编程工具和团队集成通过各自的外部 CLI 适配器连接。

## 代码组织

| 包 | 职责 |
| --- | --- |
| `cmd/silverwing` | 参数解析、组装应用与桌面入口 |
| `internal/desktop` | MyGO 视图、输入、窗口状态及异步结果回写 |
| `internal/application` | 操作边界、工作流协调、DTO 与服务组装 |
| `internal/domain` | 持久模型、枚举及业务规则 |
| `internal/workspace` | Git 仓库、隔离工作区与任务生命周期 |
| `internal/documents` | 资料路径边界、文件管理、读取、保存与草稿 |
| `internal/editor` | Markdown 原文模型、查找、撤销及自动保存状态机 |
| `internal/media` | PDFium、图片与媒体资源 |
| `internal/delivery` | Tag 交付、执行历史与可选 Genbu 状态 |
| `internal/integrations` | Meegle 需求、评论与需求缓存 |
| `internal/platform` | 系统动作、Skills、Codex 与开发工具适配 |
| `internal/executable` | 外部 CLI 路径、版本、环境与来源解析 |
| `internal/process` | 进程执行、超时、取消及子进程清理 |
| `internal/storage` | 配置分片、锁、原子写入、备份与恢复 |
| `internal/tools` | JSON、时间、文本对比等独立工具 |

## 界面与编辑

MyGO 固定为 v0.5.0，使用 `third_party/mygo` 的本地源码。字体回退、输入法、菜单、剪贴板及绘制由框架负责；本地扩展和升级注意事项见 [MYGO-PATCHES.md](../third_party/MYGO-PATCHES.md)。

任务资料和 Skills 共用 FileTree 的展示与输入组件。构树、排序、权限及文件操作仍属于宿主，选中项、展开目录与当前打开文件分别维护。

TXT、SQL 和代码直接使用公共 TextEditor；JSON、CSV 保留默认预览并可切换源码编辑。宿主持有文档身份、草稿和保存状态，组件负责显示与输入，保存回显不重建 Buffer。资料大小上限为含 BOM 的 3 MiB，开发工具为 2 MiB。

Markdown 使用 MyGO 自带的 `markdown.View` 与 `DefaultCodeBlock` 只读阅读，保留查找、原文复制、图片权限、链接及 Mermaid 渲染。原文预览使用只读 TextEditor，不提供 Markdown 编辑与保存；普通阅读不加载或改写已有 Markdown 草稿。详见 [TEXT-EDITOR-AND-FILE-TREE.md](TEXT-EDITOR-AND-FILE-TREE.md)。

## 状态与持久化

配置和任务记录存放在用户的 `silverwing` 数据目录；显式 `--home` 可使用独立目录。首次初始化采用用户任务目录，已保存的自定义根优先。开发验证必须同时隔离 home、任务根与资料根。

配置保存使用锁、分片提交与恢复日志。设置输入由 UI 线程合并并串行保存，回显前保护尚未提交的输入。任务根切换提供迁移、直接切换和取消，只有确认后的业务操作可以改变目录。

普通文本使用 `materials-text-drafts.json`，650ms 防抖自动保存，组合输入期间暂停落盘。保存验证任务及资料身份、路径、普通文件、符号链接或 junction 和基线摘要，再原子替换文件；失败或冲突保留草稿。原文的 UTF-8 BOM 及混合换行保持不变。

资料请求捕获任务、目录与文档身份，过期结果不能覆盖新页面。切换、重命名、删除及退出通过现有输入回收与保存租约协调。阅读位置缓存仅包含浏览位置和选区，不作为文件内容或草稿来源。

## 外部集成

Meegle 和 Genbu 通过外部 CLI 及用户配置接入，不在源码中内置团队地址或凭据。需求与资料请求核对已确认的账号、profile 和资源身份，旧请求结果不能回写新的上下文。资料 Git 同步按用户配置执行；Tag 交付和系统文件操作通过各自的显式动作执行。

本地研发工具可独立使用。服务进程、数据库、凭据和测试环境由用户项目管理。

## 构建与依赖

Windows 与 macOS 的构建、资源校验和打包方式见 [RELEASE.md](RELEASE.md)，PDF 与图表边界见 [NATIVE-MEDIA.md](NATIVE-MEDIA.md)。打包保留 MyGO、Mermaid、PDFium 和 Go 依赖的许可证与版权声明。
