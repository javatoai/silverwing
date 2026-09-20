# 已知问题与支持边界

本文记录 silverwing `2.0.0` 已确认的问题、测试缺口和明确接受的平台限制。这里的“已知”不代表都会在近期修复；产品支持范围变化后应同步更新本文。

## 待处理问题

### macOS DMG 未签名和公证

- **影响平台**：Apple Silicon macOS。
- **现象**：从网络下载 DMG 后，Gatekeeper 可能警告或阻止应用启动。
- **原因**：发布流程尚未配置 Apple Developer ID 签名、Hardened Runtime、公证和 staple。
- **临时处理**：内部可信分发可由用户通过 Finder 右键打开；不建议要求普通外部用户长期使用该方式。
- **状态**：待建立正式 macOS 分发身份后处理。

### macOS 终端命令依赖 Automation 权限

- **影响平台**：macOS。
- **现象**：从 silverwing 打开 Terminal 并执行 Genbu、Meegle 等命令时，系统可能首次请求控制 Terminal 的 Automation 权限；用户拒绝后命令无法自动执行。
- **原因**：当前通过 `osascript` 调用 Terminal 执行命令。
- **临时处理**：在系统设置中允许 silverwing 控制 Terminal，或复制命令后手工执行。
- **状态**：权限失败提示和引导仍可加强。

### Codex 可用状态未进行协议预检

- **影响平台**：Windows、macOS。
- **现象**：即使本机未安装 Codex，或未注册 `codex://` 协议，界面仍会将 Codex 显示为可用；点击后可能无法打开。
- **原因**：当前适配器直接调用深链接，没有可靠的跨平台协议注册检查。
- **临时处理**：确认 Codex Desktop 已安装并可处理 `codex://` 链接。
- **状态**：待增加不产生副作用的可用性探测。

## 测试与验证缺口

### macOS Genbu 权限夹具已修正，等待平台回归

当前开发代码为 Genbu 测试文件显式设置 POSIX 执行权限，并取消发布校验中的两个权限夹具排除项。已配置路径优先、重新探测后保留手工配置这两个场景将随正常 macOS 测试执行；本轮修改尚未在 macOS Runner 或实机验证。

### 发布工作流跳过部分托管环境 Git 集成测试

Windows 与 macOS 的发布工作流使用 `skipHostedGitIntegrationTests` 排除一组依赖本机 Git 行为的集成测试，以规避托管 Runner 的不稳定环境。本地完整测试仍会执行这些用例，但发布工作流对 Worktree、提交、推送、修复和 Tag 流程的覆盖不是完整的。后续应让夹具独立于 Runner 全局 Git 配置，并逐步取消排除。

## 明确接受的限制

### macOS 只支持 Apple Silicon

当前 DMG 在 ARM64 Runner 上构建，只支持 Apple Silicon Mac；Intel Mac 不在支持范围内，不计划提供 x64 或 Universal DMG。

### Windows 以 x64 为正式目标

Windows 安装包由 x64 Runner 构建。Windows ARM64 没有独立的原生构建和测试承诺；是否能通过系统兼容层运行不作为正式支持能力。

### Cursor 的 macOS 自动发现不完整

本轮开发代码已通过登录 shell 的 PATH 查找 `cursor`，改善 Finder 启动时的发现能力；但仍未增加独立的 `/Applications/Cursor.app` 扫描。若只安装应用而没有配置 shell 启动命令，仍可能显示不可用。独立应用扫描不在本轮修复范围内。

### silverwing CLI 一键安装仅支持 Windows

设置页中的 CLI 安装、卸载和用户 PATH 管理仅支持 Windows。macOS 不提供一键安装；需要使用应用资源中的 `resources/cli/bin/silverwing` 并由用户自行配置 PATH。该差异当前不安排修复。
