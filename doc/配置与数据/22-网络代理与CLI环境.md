# 网络代理与 CLI 环境

[返回知识库目录](../Index.md) · [上一篇：备份恢复与迁移](21-备份恢复与迁移.md) · [配置持久化与恢复机制](../技术架构/24-配置持久化与恢复机制.md)

本文说明 Silverwing **0.0.1 Go / MyGO 版**如何解析 Git、Codex、Meegle、Lark 和 Genbu 命令，并为子进程设置网络代理。核对日期为 2026-10-19。示意图中的命令和路径均为虚构例子。

![Silverwing 配置、CLI 路径解析、代理环境和子进程之间的关系](../assets/proxy-cli-chain.png)

## CLI 可执行文件从哪里来

解析结果按当前实现遵循下列顺序；设置中已有非空路径时，不会再用 PATH 里的另一个版本替代：

1. **读取 Silverwing 设置中的可执行文件路径**。Git、Codex、Meegle、Lark、Genbu 的非空配置路径均直接优先使用，不会因为该路径失效而继续自动探测其他位置。
2. **执行平台探测**。Windows 上使用 `where.exe` 查询命令位置；Codex 还会检查 Codex 桌面应用的安装目录。macOS 使用登录 `zsh` 执行 `command -v`，Linux 使用登录 `bash`。Lark 的程序名是 `lark-cli`，其余分别是 `git`、`codex`、`meegle` 和 `genbu`。
3. **Genbu 特例**：探测 PATH 前会先检查 Silverwing 可执行文件附近、当前工作目录和下载目录中的随包或本地 `genbu` 程序。若所有方式都找不到，最后保留平台默认命令名，让状态检查给出可读错误。

路径解析后，Silverwing 运行 `--version` 检查命令；成功的自动发现路径会写入配置，后续优先使用这个已保存路径。状态页的重新检测可刷新状态及探测缓存，但不会替换设置中非空的路径。无论该路径来自手工选择还是先前自动发现，路径失效时都会继续尝试它并报告失败；请清空路径以重新探测，或重新选择有效的可执行文件。

因此桌面应用看到的 PATH 不一定与另一个终端窗口相同。macOS 下由 Finder 启动时，Codex、Meegle 和 Lark 子进程会通过登录 `zsh` 获取 PATH，并合并命令所在目录、登录 PATH 与应用继承的 PATH；该登录 shell 查询最长等待 3 秒。Windows 的 Codex 额外检查桌面应用目录；其它平台没有同样的桌面 Codex 探测特例。若刚安装或更新 CLI 后状态仍旧，先核对已保存的路径是否失效，再决定清空重探测或重新选程序；仍不可用时检查应用启动时继承的环境。

## 在设置中验证命令

- Git：打开「设置 → Git → Git 配置」，查看命令状态和版本；路径留空表示由程序探测。
- Meegle、Lark、Genbu：打开「设置 → CLI」对应页面，查看安装状态、版本及登录状态。自动发现和手工路径以该页显示结果为准。
- Codex：在「设置 → 任务 → 任务创建区」核对 Codex CLI 状态；Codex 扩展和 MCP 还有各自的设置页。

不要因为终端里可以输入命令就认定桌面应用能找到同一个程序。Silverwing 的版本检测实际启动子进程；目录存在、PATH 中看见一项或登录状态缓存都不能代替状态页的成功结果。CLI 安装和登录依团队发行版说明完成，不要在这里照抄其它机器的绝对路径或凭据。

## 命令代理设置

打开「设置 → 代理 → 网络代理」。选择 HTTP、HTTPS 或 SOCKS5，分别填写主机和端口；地址不能带用户名密码、路径、查询参数或片段。需要认证时单独启用代理认证，并同时填写账号与密码。例外地址以逗号分隔，只填写主机、IP 或通配符，不写协议。

勾选「代理目标」决定哪些 Silverwing 子进程使用代理：

| 目标 | 主要覆盖范围 |
| --- | --- |
| Codex CLI | Codex AI 命名、扩展相关命令和 Codex app-server |
| Git | 本地仓库与远程 Git 操作 |
| 服务快捷命令 | Bootstrap 等服务初始化命令 |
| Meegle CLI | 需求、项目和登录状态读取 |
| Lark CLI | Lark 授权与登录状态读取 |
| Genbu CLI | Tag 构建和发布状态查询 |

表单变更会自动保存到 Silverwing 配置。页面上的「测试代理」只尝试连接代理主机和端口，超时为 3 秒；它不验证 HTTP 响应、代理账号密码是否有效、目标服务能否访问，也不等同于某个 CLI 的登录检查。测试前等待配置保存完成，再分别运行相关工具的状态检查。

## 子进程环境与继承

启动受控命令时，Silverwing 读取已保存配置，并为每次进程构造环境。无论目标是否勾选，都会先移除父进程已有的 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`、`NO_PROXY` 及其小写版本，避免未勾选目标意外继承桌面进程里的代理或认证信息。勾选后才为该子进程注入代理变量；配置例外地址时同时设置大小写 `NO_PROXY`。认证信息只用于构造被选目标的进程环境，错误信息会尝试隐藏代理地址和账号。

该规则仅作用于 Silverwing 启动的受控命令，不会改写 Windows、macOS 或 Linux 的用户/系统环境，也不会改变已打开的终端、IDE 或正在运行的进程。保存设置后，新启动的受控命令读取新配置；已运行的命令继续使用启动时拿到的环境。普通终端从父进程继承的 PATH 和代理变量也不由此设置页改变。

## 超时、取消与排查

每种操作有自己的超时时限：版本检测通常为 10 秒，Meegle/Lark 状态检查为 15 秒，服务快捷命令使用该命令配置的秒数；没有单独时限的通用进程默认最长 10 分钟。取消操作或超时会触发子进程清理，并在 Windows 或 Unix 平台结束对应进程树；清理失败会作为错误返回，不应当作命令已停止。

排查时依次确认：设置中的 CLI 版本检查成功；命令路径属于 Silverwing 当前进程可访问的位置；需要代理的目标已勾选；例外地址没有误匹配；测试代理 TCP 连通后，再运行具体 CLI 状态检查。不要通过修改系统级环境变量来绕开 Silverwing 的目标隔离；若代理或命令配置刚保存，启动一次新的检查以验证实际子进程结果。

## 相关阅读

- [首次配置](../入门与基础/03-首次配置.md)
- [配置项参考](19-配置项参考.md)
- [外部 CLI 与 Codex 集成](../技术架构/26-外部CLI与Codex集成.md)

源码索引：`internal/executable/resolver.go`、`internal/platform/cli_resolution.go`、`internal/platform/cli.go`、`internal/platform/exec.go`、`internal/workspace/proxy_environment.go`、`internal/workspace/custom_command.go`、`internal/process/runner.go`、`internal/desktop/settings_environment_ui.go`、`internal/desktop/settings_save_proxy.go`、`internal/domain/models.go`、`internal/domain/validation.go`。
