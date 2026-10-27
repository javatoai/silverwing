# silverwing-product

产品人员专用分支：`release/snb-product`。直接基于 QA 分支 `release/snb-qa` 的 `455f0cf850fb`。

与 QA 版功能保持一致：任务、需求与评论、资料和资料 Git 同步、Codex、Skills、插件、MCP、说明模板及全部本地实用工具。

应用名 `silverwing-product`，数据根目录 `~/silverwing-product`，配置和任务标识 `edition: product`。Windows 安装标识、MSI GUID、注册表和快捷方式独立；macOS Bundle ID 为 `io.github.javatoai.silverwing.product`。

发布标签为 `product-vX.Y.Z`，资产前缀为 `silverwing-product-`。产品版发布同样不设为仓库 latest，不导入开发版或 QA 版配置与任务。

本次没有增加或删除 QA 功能。后续仅在产品分支维护产品使用能力。
