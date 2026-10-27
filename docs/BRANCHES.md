# 维护分支

| 分支 | 用途 | 应用名 | 发布标签 |
| --- | --- | --- | --- |
| `release/snb` | 开发版 | `silverwing` | `vX.Y.Z` |
| `release/snb-qa` | 测试版 | `silverwing-qa` | `qa-vX.Y.Z` |
| `release/snb-product` | 产品版，继承 QA 功能 | `silverwing-product` | `product-vX.Y.Z` |

QA 基线为开发版已提交的 `23fa44b`。产品分支从完成的 QA 代码继续，功能保持一致，独立安装、独立数据和发布渠道。

QA 与产品分支后续专门维护各自使用者的能力。引入开发版公共修复时，检查导航、配置、后台 Action、数据格式、说明模板与安装包边界，避免重新引入服务、代码仓库、Worktree、Tag 或生产发布。
