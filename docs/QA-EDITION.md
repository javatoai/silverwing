# silverwing-qa

测试人员专用分支：`release/snb-qa`。基线：`release/snb-dev` 的 `23fa44b0c647`。

保留需求与评论、任务与归档、资料及 Git 同步、Codex、Skills、插件、MCP、说明模板及全部本地实用工具（包括端口查询与结束进程）。任务内容只有资料、需求详情和需求说明三个页签，默认资料。

删除代码仓库、服务模块、Worktree、代码 Git 操作、Tag、Genbu、生产发布、IDE 和 Cursor；对应界面、后台 Action、配置模型及开发工作区实现均移除。

## 独立身份

- 应用、可执行文件与包名：`silverwing-qa`。
- 默认 home：`~/silverwing-qa`；默认任务和资料位于 home 下。显式 `--home` 同样隔离默认目录。
- 配置与任务标识：`edition: qa`。不自动导入开发版数据。
- QA 发布标签：`qa-vX.Y.Z`；发布资产前缀：`silverwing-qa-`；发布显式不设为仓库 latest。
- Windows 使用独立路径、快捷方式、注册表、互斥和 MSI GUID；macOS Bundle ID：`io.github.javatoai.silverwing.qa`。

## 验证

Go 全包回归、QA 专用生命周期与目录迁移检查、Python 发布与 Windows 图标测试已在本地执行。Windows x64 ZIP、Setup EXE、MSI 打包成功，并核对 MSI 产品名与 UpgradeCode 隔离。浅色和深色界面检查覆盖任务、设置和创建任务窗口；原生程序在隔离数据目录启动和截图后正常退出。

已验证 macOS arm64 交叉编译；尚未在 macOS 上生成或运行 DMG。未实际安装或卸载 Windows 安装包。没有发布标签或 GitHub Release。

产品分支 `release/snb-product` 从完成的 QA 代码继续，功能保持一致，应用名 `silverwing-product`，安装、数据与发布渠道独立。
