# QA 构建与分发

本文件仅描述打包流程，不代表已发布版本。当前产品版本来自 `internal/domain/models.go`。

## 本地验证

```powershell
go test -timeout 10m ./...
python -X utf8 -m unittest discover -s scripts -p "test_*.py"
pwsh -File scripts/build-windows.ps1 -PortableOnly
```

Windows 构建和打包需 PowerShell 7，请使用 `pwsh` 运行脚本。完整打包还需 NSIS 3.13 和 WiX 3.14.1，可用 `-NSISPath` 与 `-WixBinPath` 指定。脚本生成 x64 portable ZIP、Setup EXE、MSI，并保留原生依赖、图标与第三方声明。免安装目录为 `silverwing-qa`，入口为 `silverwing-qa.exe`。

macOS Apple silicon 上运行 `bash scripts/build-macos.sh`，生成 `silverwing-qa.app` 和 DMG。Bundle ID 为 `io.github.javatoai.silverwing.qa`。构建采用 ad-hoc 签名，未配置 Developer ID 或公证。

## 渠道边界

- 分支 CI：`release/snb-qa`。
- 发布触发标签：`qa-vX.Y.Z`，必须与源码版本完全相符。
- 四种资产前缀：`silverwing-qa-X.Y.Z-`，另生成 `SHA256SUMS.txt`。
- 发布脚本校验事件、仓库、远程标签 SHA、包列表、大小与摘要，不覆盖已有发布。
- QA 发布显式 `make_latest=false`，避免覆盖开发版默认下载。
- Windows 安装路径、快捷方式、卸载注册表、安装互斥与 MSI GUID 均独立；home 为 `~/silverwing-qa`。

只有明确授权发布后才推送标签或执行远程发布。安装和卸载验证需在可控环境中单独完成；打包成功不等于已安装验证。
