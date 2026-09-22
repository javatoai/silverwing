# 版本升级规则

silverwing 使用 `MAJOR.MINOR.PATCH` 版本号，产品版本、Gradle、安装包、Git Tag 与 CHANGELOG 必须一致。任务清单 `silverwing.json` 使用同一版本线的字符串 `schemaVersion`；八个配置分片独立使用固定的严格整数 `schema: 2`。

- 不影响任务清单或配置分片持久化字段的更新，PATCH +1，例如 `2.0.0` 到 `2.0.1`。
- 新增、删除、重命名、改变类型或语义的任务清单字段，或改变任一分片字段/归属，MINOR +1 且 PATCH 归零，例如 `2.0.1` 到 `2.1.0`。
- 大改版由产品负责人判断，MAJOR +1 且其余归零，例如 `0.5.3` 到 `1.0.0`。

`AGENTS.md`、日志、Tag 历史和构建产物不属于 schema 字段。相同 `MAJOR.MINOR` 的不同 PATCH（如 `2.0.0` 与 `2.0.1`）任务清单兼容读取，下一次正常保存时会写为当前 PATCH；不同主版本或次版本始终严格拒绝，不自动迁移。配置分片始终只接受 `schema: 2` 与其各自的已知字段。

`2.0.0` 是 silverwing 的新持久化边界。程序不读取、迁移或改写旧产品的配置、任务清单、锁文件、临时目录或过程文档。要转移 silverwing 自身配置，请通过设置页导出 `silverwing-config-<timestamp>.zip`，并在导入前完成完整性校验。
