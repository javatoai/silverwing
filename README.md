<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/readme-brand-dark.png">
    <img src="docs/assets/readme-brand-light.png" width="640" alt="Silverwing">
  </picture>
</h1>

<p align="center"><strong>基于 Go 与 <a href="https://github.com/egoist/mygo">MyGO</a> 构建的原生多仓库研发工作台 (no webview, no Electron)。</strong></p>

<p align="center">
  <a href="https://github.com/javatoai/silverwing/releases"><strong>下载</strong></a> &nbsp;·&nbsp;
  <a href="#产品演示">产品演示</a> &nbsp;·&nbsp;
  <a href="#界面导览">界面导览</a> &nbsp;·&nbsp;
  <a href="#快速开始">快速开始</a> &nbsp;·&nbsp;
  <a href="#开源协议">开源协议</a> &nbsp;·&nbsp;
  <a href="README.en.md">English</a>
</p>

<p align="center">Silverwing 是一款小巧的多仓库研发工作台，使用 Go 编写，基于 <a href="https://github.com/egoist/mygo">MyGO</a> 构建原生桌面界面，不依赖 WebView、Electron 或 JVM。<br>
安装包约 18 MB，将任务、仓库、分支和资料组织在一起，内置 Git 审阅与常用研发工具。</p>

一个需求经常需要同时修改多个服务。Silverwing 将这些服务的工作区组织为一个任务，让你在代码、需求、资料和编程工具之间切换时，始终保留完整上下文。

> Silverwing 正在持续开发中，部分功能和平台行为仍需进一步验证。

## 产品演示

36 秒了解公开版：从需求创建任务，使用 Git Worktree 自动准备工作区，再到 Git 审阅、任务资料，以及一键在 Codex 中打开项目。

https://github.com/user-attachments/assets/ee910d3b-e298-4061-9c59-fb2aa8b4c6f7

含中文旁白与配乐。点击播放，可暂停、拖动进度或全屏观看；播放器默认静音，请点击声音按钮开启。视频使用 Windows 浅色界面与示例数据，当前任务创建需要连接 Meegle。[打开演示页面](https://github.com/javatoai/silverwing/blob/master/README.md#产品演示)。

## 功能

- **任务工作区**：将多个仓库和模块归入同一任务，支持 Git Worktree 与独立克隆。
- **任务导航**：按任务名称、需求编号、项目组或分支搜索，置顶常用任务，并让置顶任务始终排在列表前面。
- **Git 审阅**：按配置主分支、本地未提交文件或提交范围查看差异，通过变更文件树与左右对比阅读修改。
- **Agent 上下文**：汇总全局、项目组和任务说明，生成 `AGENTS.md`，并在 Codex、Cursor 或 IDE 中打开对应工作区。
- **任务资料**：浏览本地文件，阅读 Markdown、PDF、图片、表格和图表，支持查找、源码查看与文档链接。
- **Skills 与 Codex 配置**：浏览本机 Skills，管理 Git 来源的 Skill、Codex 插件，并查看 MCP 配置。
- **研发工具**：JSON 格式化与折叠、日期和时间戳转换、左右文本对比，以及可选的本机端口占用查询。
- **集成工作流**：接入 Meegle 需求和评论，显式同步选定资料到飞书。
- **本地工作区管理**：配置与任务记录保存在本机，支持浅色／深色主题，归档任务时保留工作区。

## 界面导览

<table>
  <tr>
    <td width="50%" valign="top"><strong>仓库与服务</strong><br>
      按项目组组织本机仓库与服务模块。<br><br>
      <a href="docs/assets/readme-repositories.png"><img src="docs/assets/readme-repositories.png" alt="Silverwing 本机仓库、服务和项目组管理界面"></a></td>
    <td width="50%" valign="top"><strong>Git 审阅</strong><br>
      从变更文件树定位代码，左右对比修改前后的内容。<br><br>
      <a href="docs/assets/readme-git-review.png"><img src="docs/assets/readme-git-review.png" alt="带变更文件树、增删行和行内高亮的左右 Git 差异界面"></a></td>
  </tr>
  <tr>
    <td valign="top"><strong>任务资料</strong><br>
      将设计说明、参考文档和实施上下文放在任务旁边。<br><br>
      <a href="docs/assets/readme-materials.png"><img src="docs/assets/readme-materials.png" alt="任务资料文件树与原生 Markdown 阅读器，展示表格和代码块"></a></td>
    <td valign="top"><strong>研发工具</strong><br>
      在同一个桌面应用中处理 JSON、时间戳和文本差异。<br><br>
      <a href="docs/assets/readme-developer-tools.png"><img src="docs/assets/readme-developer-tools.png" alt="Silverwing JSON 工具，包含语法着色、行号和折叠入口"></a></td>
  </tr>
</table>

<sub>截图来自 Windows，使用示例数据。应用界面目前为中文，两份 README 共用同一组截图。</sub>

## 快速开始

### 下载

可从 [GitHub Releases](https://github.com/javatoai/silverwing/releases) 下载已发布的安装包，也可以按照[源码构建说明](docs/RELEASE.md)自行构建。支持以下安装包：

| 平台 | 安装包 |
| --- | --- |
| Windows x64 | Setup EXE、MSI 或免安装 ZIP |
| macOS Apple silicon（arm64） | DMG |

Windows 免安装版需要完整解压，再运行 `silverwing/silverwing.exe`；请将 `resources` 目录保留在程序旁。安装包已包含 PDF 和图表所需的原生依赖，运行时无需安装 Go 或 JVM。

macOS 应用使用 ad-hoc 签名，**尚未进行 Developer ID 签名或 Apple 公证**。系统可能拦截首次启动；核实下载来源后，可按需通过系统“隐私与安全性”设置允许打开。目前不提供 Intel Mac、Windows ARM 或 Linux 安装包。

### 准备工作区

1. **添加本地仓库**：进入“服务”管理仓库，添加本机已有的 Git 仓库，配置服务／模块及对应主分支。
2. **选择开发工具**：配置 Git，以及你使用的 IDE 或编程 Agent。Codex、Cursor 等外部工具需要单独安装。
3. **体验本地工具**：无需连接需求或部署服务，即可使用 JSON、日期时间和文本对比。

### 从需求创建任务

当前的任务创建入口位于“需求”页，需要连接 Meegle 项目。在设置中接入 Meegle 后，选中需求并点击“基于需求创建任务”，核对任务名称、分支和服务范围，创建后即可在 IDE 或 Agent 中打开对应工作区。

飞书资料同步是可选能力。接入 Meegle 或飞书之前，可以先配置仓库并使用本地研发工具。Silverwing 提供编程工具的打开入口，不会自动运行多个 Agent 会话。

### 查找和切换任务

- 在任务列表上方的“搜索任务”中输入关键词。搜索不区分英文大小写，空格分隔的多个关键词需要同时匹配，也会检索已经读取的需求标题和关联服务。
- 任务列表直接显示当前分类中符合搜索条件的全部任务，置顶任务排在前面且不重复。右键任务可置顶或取消置顶，已置顶任务名前常驻显示强调色图钉；右键和置顶不会切换当前任务。
- 任务右键菜单集中提供在 Codex 中打开、批量合并主分支、资料目录操作、归档、恢复归档和删除；详情页不再显示“更多”按钮。操作针对右键任务，恢复成功后保留归档列表及其筛选条件。
- 研发任务和归档任务各自保留本次会话的搜索条件和列表位置；置顶偏好会在重启后保留。
- 在搜索框中按 `Esc` 清空关键词，按方向键定位任务，再按 `Enter` 打开；任务行也支持 `Home`、`End` 和空格键。新建任务后，列表会自动显示并定位到该任务。

## 集成

| 集成 | 用途 | 接入条件 |
| --- | --- | --- |
| Git | 仓库检查、隔离工作区、差异审阅与显式交付操作 | 本机安装 Git |
| Codex／Cursor | 携带相应 Agent 上下文打开任务工作区 | 安装并配置需要使用的工具 |
| Meegle／飞书项目 | 需求列表、任务创建、需求详情与评论 | 接入 Meegle CLI 和项目账号 |
| 飞书 | 将任务资料显式同步到配置的知识库位置 | 配置 Lark CLI 权限与目标目录 |

Silverwing 管理代码工作区及其上下文。服务进程、数据库、容器、凭据和测试环境仍由各项目自行管理。

## 文档

| 文档 | 语言 | 内容 |
| --- | --- | --- |
| [使用与开发知识库](doc/Index.md) | 中文 | 入门配置、日常操作、集成、架构与开发维护 |
| [架构说明](docs/ARCHITECTURE.md) | 中文 | 原生界面、业务包、存储与集成边界 |
| [分支说明](docs/BRANCHES.md) | 中文 | 公共版与 SNB 内部版的功能边界和同步规则 |
| [原生媒体](docs/NATIVE-MEDIA.md) | English | PDF 和图表资源、渲染方式与平台限制 |

## 开源协议

Silverwing 使用 [MIT License](LICENSE)。第三方依赖保留各自的许可证和版权声明。

English documentation: [README.en.md](README.en.md).
