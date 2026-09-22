# 本地开发指南

## 前置条件

- JDK 21；
- Windows 开发机使用 PowerShell 和 `gradlew.bat`；
- macOS/Linux 使用可执行的 `./gradlew`；
- 打 Windows MSI 时需要 WiX Toolset；
- Meegle、Codex、Cursor 均为可选本地集成，缺失不影响应用启动。

## 常用命令

Windows：

```powershell
.\gradlew.bat fastTest
.\gradlew.bat :desktop:compileKotlin
.\gradlew.bat :desktop:run
```

macOS/Linux：

```bash
./gradlew fastTest
./gradlew :desktop:compileKotlin
./gradlew :desktop:run
```

`desktop:run` 会启动本地 Compose Desktop 开发版。运行中的应用会占用构建输出；执行 `clean`、安装包构建或修改打包资源前，先关闭开发版。

## 代码边界

```text
core/
  domain/          稳定模型与规则
  application/     用例、端口与工作流编排
  infrastructure/  Git、JSON、文件、进程和外部 CLI 适配
desktop/            Compose 界面与平台集成
cli/                只面向 Agent 的 JSON 命令行入口
```

Compose 只读取 UI 状态并发出回调；Git、JSON、文件写入、剪贴板和进程调用由 Application/Infrastructure 或 Desktop 平台适配器处理。新增外部工作区工具或交付动作应实现现有注册表接口，而不是在任务创建主流程添加工具专属判断。

## 测试要求

提交前至少运行：

```powershell
.\gradlew.bat fastTest :desktop:compileKotlin
```

`fastTest` 会跳过会创建仓库、clone 与 worktree 的慢速 Git 集成测试，适用于日常开发。需要改动 Git 生命周期、删除、归档、Tag 或工作区策略时，必须额外运行完整 `test`；涉及 Compose 交互时，至少启动开发版进行人工验证。修改发布相关文件时，运行对应平台的打包脚本。

## 本地数据与测试数据

应用数据默认保存于 `~/silverwing`，默认任务根目录为 `~/silverwing/tasks`，配置分片位于 `~/silverwing/config/`，不在仓库内。不要将真实配置分片、任务清单、迁移日志、日志、Meegle 输出或任何凭据提交到 Git。

配置和任务 schema 是严格版本化的。旧 schema 会被拒绝且原文件保持不变；不要为了本地调试而手工降低 schema 版本。
