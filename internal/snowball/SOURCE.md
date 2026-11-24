# Snowball 平台客户端源码

源仓库为 `ai-shared-toolkit`，读取时 HEAD 为 `48adaa786450af734cfe133bf62aa8ed0b241049`。Genbu 采用该工作区当时的完整 API 源码，包含尚未提交的 Tag、审批、配置快照和编辑实现。

| 来源 | Silverwing 包 | 来源快照 SHA-256 |
| --- | --- | --- |
| `packages/genbu/api` | `genbuapi` | `108167d449101cdb97ddefee71bc61ee18857916299cab43ab633649476777c9` |
| `packages/dragon/api` | `dragonapi`，含 `applications.json` | `833a1ae3969aefa8e3927d7d344959dd3ff4db25bc32638a673548d3daad9344` |
| `packages/snowball-auth/store.go` | `auth` | `53e9f807b2cdac0e48bce248d8568ff3e8e01ebd507732e375f00523a631ad24` |

API 目录哈希由文件名排序后的 `文件名 SHA-256` 清单计算，清单采用 UTF-8 和 LF，无末尾换行。账号源文件使用原始字节的哈希。

Silverwing 为客户端注入 HTTP 传输、连接作用域和共享凭据。适配包括请求发出前的鉴权检查、写入回执分类及敏感信息脱敏。账号来自原工具的 `~/.snowball-config/credentials.json`，在 Windows 上位于 `%USERPROFILE%\.snowball-config\credentials.json`。「保存账号」明确写入同一文件，各 `--conf-dir` 仅分别保存连接开关。

Silverwing 独立维护内置客户端的登录会话。旧配置目录中的 `genbu.json` 原样保留。原工具继续负责自己的命令行入口、终端交互和会话缓存。

## UAT 发版与重启

UAT 代码取自 `ai-shared-toolkit` 的本地工作区，当时 HEAD 仍为 `48adaa786450af734cfe133bf62aa8ed0b241049`。下表为实际读取的 `packages/genbu/api` 源文件原始字节的 SHA-256。Silverwing 沿用原实现的目标选择、发布参数和回执判断，并将预检、写入许可和未知结果接入自己的客户端。

| 来源文件 | SHA-256 |
| --- | --- |
| `uat_pod_operations.go` | `e1af4940f213973c1c39afa2dc1faaff662b5ebbf8ac42dfb7eee66057f27690` |
| `uat_pod_operations_test.go` | `3c5e2700b44f92f2e3bc71a63bb2776d351414ec9fd66c65600fe43863d4a877` |
| `uat_release.go` | `b7cc9ee7ec93ca95652f9c66f1fafb2493fca7ab4b6d99e685ac896bca27ef39` |
| `uat_release_test.go` | `8b89d8d8b56616e3aa6b928913f7a12480e6ba5ba2905edfc71ccbf677209d18` |
| `client.go` | `ccda1c29cd694d0e5a11ddaf0e257930aa7742029f7906ee7e1a58304283ef1f` |
| `models.go` | `3a28818404d050567717e5356fdbadeb8eb5357dcf433989aef31a6314ac1c50` |

自动开启 UAT 发布开关的请求来自 Genbu Web 版本流水线页面。页面通过 `POST /genbu/version/assign/baseInfo` 提交 `version_id`、步骤名 `env` 和 `enable: 1`。Silverwing 在确认发版后按需执行这次写入，随后用同一路径的 GET 核对原版本，并复核发版目标与配置。

## UAT 配置审批

审批差异和同意请求沿用 Genbu Web 审批管理页面。该页面以审批内容作为申请配置，通过 `GET /genbu/basic/api/configMapDetail` 按环境、应用、命名空间和 ConfigMap 名称读取当前配置。操作权限由 `GET /genbu/admin/api/user/operation` 返回。同意使用 `PUT /genbu/basic/api/approve` 提交审批编号、`status: 1` 和原审批备注。
