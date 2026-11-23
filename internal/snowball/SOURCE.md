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
