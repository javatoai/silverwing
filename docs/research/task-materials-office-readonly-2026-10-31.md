# 任务资料模式 Word / PPT 原生只读预览方案

日期：2026-10-31

## 当前约束与建议

用户要求在任务资料中直接只读预览 Word、PPT，尽量保留原始排版、允许少量差异；不要求用户安装 LibreOffice，不嵌入 WebView，并控制主安装包体积。此前的系统 WebView 建议已排除。

可验证的原生路线是 **转换组件 → 缓存 PDF → Silverwing 现有 PDFium 阅读器**。`office2pdf` 目前仅作为试验候选，不作为已经选定的正式依赖；用户同时关注首次延迟和项目可靠性。转换组件可由软件管理，首次使用时下载；用户不需要手动安装办公套件或 Rust。离线发行则需随包带组件，安装包会相应增长。

## 已核实的原生候选

[`developer0hye/office2pdf`](https://github.com/developer0hye/office2pdf/tree/v0.10.0) 是 Rust/Typst 转换库与 CLI。维护者提供原生独立程序及库 API，输入 DOCX、PPTX、XLSX，输出 PDF，不使用 LibreOffice、Chromium 或 Docker。当前核对版本是 2026-10-28 发布的 **v0.10.0**；其项目许可证为 Apache 2.0。[固定版本说明](https://github.com/developer0hye/office2pdf/blob/v0.10.0/README.md) · [许可证](https://github.com/developer0hye/office2pdf/blob/v0.10.0/LICENSE) · [发布记录](https://github.com/developer0hye/office2pdf/releases/tag/v0.10.0)

该版本明确列出的格式是 `.docx/.pptx/.xlsx`，没有 `.doc/.ppt` 支持承诺。首期应明确为现代 Word/PPT 格式预览，旧二进制格式另行选择引擎。

维护者列出 DOCX 段落、格式、表格、图片、页眉页脚，以及 PPTX 文本框、形状、表格、图片、母版等支持。它仍有未覆盖和近似处理的排版特性，例如部分图片、复杂分节分页、图形导入边界。支持格式不代表每份文件都能控制在“少量差异”；中文字体、公式、图表、SmartArt 和复杂布局必须用真实资料比较。当前 PDF 阅读器按静态页显示，不播放 PPT 动画、切换或嵌入音视频。[能力与兼容边界](https://github.com/developer0hye/office2pdf/blob/v0.10.0/README.md#supported-formats)

## 体积与分发

下面是 GitHub v0.10.0 官方发布 API 中的 `assets[].size`，不是本机实测，也不是最终 Silverwing 安装包增量：

| 目标平台 | 官方压缩发行包 | 字节数 | 约 MiB |
| --- | --- | ---: | ---: |
| Windows x64 | `office2pdf-v0.10.0-x86_64-pc-windows-msvc.zip` | 30,057,328 | 28.66 |
| macOS arm64 | `office2pdf-v0.10.0-aarch64-apple-darwin.tar.gz` | 29,999,466 | 28.61 |

来源：[固定发布 API](https://api.github.com/repos/developer0hye/office2pdf/releases/tags/v0.10.0) · [发布页面](https://github.com/developer0hye/office2pdf/releases/tag/v0.10.0)。本轮已下载并核对 Windows 压缩包 SHA-256：`3da06d045d69562ca00757716269d4f000c4acf74d4137e401874898bfa3145a`。解压后的 EXE 为 71,944,192 字节，约 68.61 MiB；该数字也不是最终 Silverwing 安装包增量。macOS 解压占用、最终安装包增量和内存均未测。

优先考虑按需组件：主安装包只携带转换调度代码；首次预览时下载固定平台与版本的原生程序，校验来源和 SHA-256，之后本机离线转换。代价是首次使用需要网络、组件仍占磁盘。需要全新机器上完全离线可用时，应将组件打入安装包。macOS 上还要验证 helper 的签名与发行方式。

官方允许用 `--no-default-features` 省去 Typst 默认嵌入字体，但还有其他依赖与回退字体，不能据此预估最终体积。删减字体也可能影响中文、公式和分页，先测兼容性再裁剪。[构建与字体选项](https://github.com/developer0hye/office2pdf/blob/v0.10.0/README.md#native-builds-without-typsts-bundled-fonts)

## Silverwing 接入位置

- 在 [Kind](../../internal/documents/files.go#L66) 与 [ReadMaterialsPreview](../../internal/documents/files.go#L172) 增加 Office 分支，避免把它送入未知纯文本检测与 3 MiB 文本限制；Office 输入和派生 PDF 另设明确预算。
- 完成现有资料根目录与路径授权，读取稳定源快照到隔离工作目录。后台执行转换，避免长时间占用 [documentAction 的 WithTask](../../internal/application/document_actions.go#L28)。Go 直接传子进程参数，输出仅写缓存，提供超时、取消及错误反馈。
- 扩展 [document.read 结果处理](../../internal/desktop/materials_action_results.go#L38)，将原文件路径、类型、版本和派生 PDF 路径分开；切换任务或文件后丢弃过期结果。标签和文件树一直使用原 Office 文件身份，预览不提供编辑与保存入口。
- 复用 [原生媒体打开](../../internal/desktop/native_media.go#L124)、[PDF 页面视图](../../internal/desktop/native_media_pdf.go#L58)、缩放与当前文档查找。缓存键纳入源内容哈希、引擎版本、转换配置与字体环境；原件变化后重建。派生缓存不写入资料树或资料 Git 仓库。
- 现有 [资料目录全文检索](../../internal/documents/fulltext.go#L195) 独立按扩展名处理，新增 Office 预览不会自动让 Office 原件进入目录检索。失败时复用 [现有提示与系统打开入口](../../internal/desktop/materials_preview_fallback.go#L11)。

## 其他符合原生方向的选择

| 路线 | 适用情况 | 主要代价 |
| --- | --- | --- |
| 内置原生商业 SDK | 若真实文件兼容性或旧 `.doc/.ppt` 覆盖不满足要求，可评估 Apryse Office 转换、Aspose.Words/Slides C++ | 需要商业许可；字体、平台发行包及最终体积都要实测，不能因为是 C++ 就称轻量 |
| 自建服务转换后返回 PDF | 若主安装包和本机新增组件都要尽可能少，可将转换放到自有服务 | 文档需上传，依赖网络与服务部署；客户端仍可使用现有原生 PDF 阅读器 |
| 自行解析 OOXML 并原生绘制 | 若明确只支持有限文档结构，可用 Go/Rust 实现受限预览 | 需要自己处理排版、字体、分页、母版与图形，开发维护成本高，无法先承诺仅有少量差异 |

Apryse 应核对专用 `OfficeToPDF` / `StreamingPDFConversion` API，避免混用可能依赖系统应用的其他转换路径。[原生 C++ API](https://sdk.apryse.com/api/PDFTronSDK/cpp/classpdftron_1_1_p_d_f_1_1_convert.html)。Aspose 的 Word/PPT 是两个原生产品，分别核对目标平台和发行体积。[Words 转 PDF](https://docs.aspose.com/words/cpp/convert-a-document-to-pdf/) · [Slides 转 PDF](https://docs.aspose.com/slides/cpp/convert-powerpoint-to-pdf/)

浏览器渲染库这轮不采用。`@silurus/ooxml` 的 Rust/WASM 解析器与 TypeScript Canvas 渲染器也不能直接当作现成的 Go 原生绘制库。[维护者架构说明](https://github.com/yukiyokotani/office-open-xml-viewer)

## 本轮验证范围

已核对维护者说明、固定版本许可证、GitHub 发布元数据、本地接入代码及转换源代码，运行下述合成样本基准，并完成同一已有程序内容加入组件前后的 Windows ZIP、Setup EXE、MSI 重打包。未修改应用代码或现有打包脚本；真实 Word/PPT 的版式保真、复杂兼容性、最终集成版包体、内存和跨平台运行仍未验证。正式选型前需用代表性实际资料对比原文及 Office 导出的 PDF。

## 首次预览延迟边界

固定到 v0.10.0 的源码显示，DOCX/PPTX 转换是同步整份流程：读取和解析输入、生成渲染源、编译完整 PDF 字节后才返回；没有 DOCX 页范围或部分 PDF 回调。PPTX 有 `slide_range`，解析器会跳过范围外的幻灯片正文，但仍需处理演示文稿元数据、主题等，并最终完成所选幻灯片的 PDF。因此它能缩小输出范围，不能承诺先显示第一页。若 Silverwing 采用 helper 生成缓存 PDF，阅读器要等该次转换完成后再打开派生文件。[转换管线](https://raw.githubusercontent.com/developer0hye/office2pdf/e8d7378/crates/office2pdf/src/lib_pipeline.rs) · [转换选项](https://raw.githubusercontent.com/developer0hye/office2pdf/e8d7378/crates/office2pdf/src/config.rs) · [PPTX 幻灯片筛选](https://raw.githubusercontent.com/developer0hye/office2pdf/e8d7378/crates/office2pdf/src/parser/pptx.rs)

常驻进程可能复用 renderer 的系统字体索引、字体槽和 Typst 缓存；源码也显示字体数据按需读取。但某些文档每次转换仍会建立字体搜索上下文并发现字体，所以不能据此保证重复转换免去全部字体扫描或必然更快。[字体与 renderer 缓存](https://raw.githubusercontent.com/developer0hye/office2pdf/e8d7378/crates/office2pdf/src/render/pdf.rs) · [逐次字体上下文解析](https://raw.githubusercontent.com/developer0hye/office2pdf/e8d7378/crates/office2pdf/src/render/font_context.rs) · [管线中的字体上下文调用](https://raw.githubusercontent.com/developer0hye/office2pdf/e8d7378/crates/office2pdf/src/lib_pipeline.rs)。这些是源码边界，不是性能或长期稳定性实测；候选引擎仍应对代表性资料测首次启动和后续转换耗时。

## 本机合成样本耗时

环境为 Windows x64、Intel i7-11800H（8 核 16 线程）、31.7 GiB 内存。测试 v0.10.0 官方 EXE，每个样本重新启动进程转换三次，每次写全新的 PDF，计时覆盖进程启动至退出。操作系统文件缓存没有清空，也未控制其他进程负载；这不是严格的冷缓存基准。下载、样本生成、结果检查、Silverwing PDF 首屏打开均不计入时间。

| 合成样本 | 输入大小 | 三次耗时范围 | 中位数 |
| --- | ---: | ---: | ---: |
| Word 5 页，中文/表格/图片 | 0.26 MiB | 0.346–0.825 秒 | 0.393 秒 |
| Word 30 页，中文/表格/图片 | 0.27 MiB | 0.568–0.628 秒 | 0.593 秒 |
| Word 100 页，中文/表格/图片 | 0.27 MiB | 1.234–1.267 秒 | 1.250 秒 |
| PPT 10 页，20 张独立图片/表格/形状 | 4.60 MiB | 0.738–0.799 秒 | 0.751 秒 |
| PPT 50 页，100 张独立图片/表格/形状 | 22.91 MiB | 2.847–2.874 秒 | 2.869 秒 |

Word 样本使用显式分页并复用同一张图片，文件小且排版规则，不能代表自由流分页、图片环绕、公式或复杂图表文档。PPT 图片为固定种子生成的图像。15 次调用均正常退出；PDF 页数符合预期，首尾页编号标记及首页中文可提取。这些检查验证基本输出，不证明视觉保真或长期可靠性。

可复核材料：[基准脚本](../../build/research/office-preview-latency-20261031/benchmark.py) · [原始结果](../../build/research/office-preview-latency-20261031/runs/20261031T101934121218Z/results.json) · [机器信息](../../build/research/office-preview-latency-20261031/environment.json) · [发布元数据快照](../../build/research/office-preview-latency-20261031/source-metadata.json)。材料位于 Git 忽略的 build 目录，没有暂存二进制或样本。

## 项目成熟度与选型结论

官方仓库创建于 **2026-02-26**，首个发行版 **v0.1.0 发布于 2026-02-27**；本次测试版 **v0.10.0 发布于 2026-10-28**。截至本报告日期，公开维护历史约八个月，不能仅凭功能清单或这组速度结果认定适合正式接入。[仓库元数据](https://api.github.com/repos/developer0hye/office2pdf) · [首个发行版](https://github.com/developer0hye/office2pdf/releases/tag/v0.1.0) · [本次测试版本](https://github.com/developer0hye/office2pdf/releases/tag/v0.10.0)

维护者 README 提供兼容性边界、外部 Word 参考样本和视觉检查方法，这是已有验证机制的证据，不能替代 Silverwing 实际资料验收。项目较新并不直接证明不稳定，现有结果同样不能证明它已经稳定。[维护者兼容性验证说明](https://github.com/developer0hye/office2pdf/blob/v0.10.0/README.md#docx-compatibility-triage)

当前判断：速度在本机规则样本中可接受，但真实兼容性与维护可靠性仍不足以定案。若继续评估，应把转换引擎做成可替换的隔离组件，固定版本，用代表性资料验收后再决定是否默认开启；同步评估原生商业 SDK 或自建转换服务的成本与限制。现阶段不把 office2pdf 作为唯一的正式预览依赖。

## Windows 内置组件的包体增量（A/B 实测）

以本机已有的 `0.0.7-20261029T175626Z` Windows 程序内容为相同基线，用当前项目打包配置制作两组产物：一组保持原内容，另一组只增加 v0.10.0 的 `office2pdf.exe`、上游 README 和项目 LICENSE。没有重新编译应用、运行安装器或发布产物。ZIP 使用当前 `Compress-Archive -CompressionLevel Optimal`，Setup 使用项目 NSIS 脚本与 NSIS 3.13，MSI 使用项目 WiX 配置与 WiX 3.14.1。

| 产物 | 基线大小（MiB） | 加组件后（MiB） | 增量（MiB） | 增量字节 |
| --- | ---: | ---: | ---: | ---: |
| Windows 免安装 ZIP | 18.078 | 46.747 | **28.669** | 30,061,374 |
| Windows Setup EXE | 17.657 | 45.428 | **27.771** | 29,119,630 |
| Windows MSI | 17.688 | 45.473 | **27.785** | 29,134,848 |

新增解压文件合计 71,977,336 字节，约 **68.64 MiB**；使用时还会产生文档预览缓存。原始 GUI 文件与打包脚本的测试前后 SHA-256 保持一致。基线包为既有版本，表中总包体不能当作当前开发中代码的正式发行大小，但能隔离观察此组件的压缩增量。尚未包含未来集成代码、完整第三方声明、签名等最终发行材料；macOS 的 DMG 增量未实测，前述 28.61 MiB 是上游 tar.gz 大小。

可复核材料：[测量脚本](../../build/research/office-preview-package-size-20261031/measure-windows.ps1) · [A/B 原始结果](../../build/research/office-preview-package-size-20261031/run-20261031T103135893Z/results.json)。重打包产物、日志及程序内容副本都在该测试目录内，没有暂存或发布。

对分发的建议：若主包体积优先，采用按需下载组件，主安装包无需携带上述约 28 MiB 内容；下载调度代码尚未实现，其自身增量未测。可另提供包含组件的完整离线包，供首次使用也没有网络的用户选择。

## 国内可达的按需下载

可把 GitHub 保留为 CI 上游，在发布流程将固定版本的 helper 上传到自有 HTTPS 下载域名或国内对象存储（阿里云 OSS、腾讯云 COS）。应用运行时只访问该下载源，不要求用户浏览器打开链接或使用代理。COS 的公开对象 URL 可匿名下载；若对象设为私有，则要使用签名 URL。也可将自有域名绑定为 COS 源站域名并启用 HTTPS，不必开 CDN；如需国内 CDN，域名需完成 ICP 备案。[COS 对象访问 URL](https://intl.cloud.tencent.com/zh/document/product/436/45504) · [COS 自定义域名与 HTTPS/CDN 条件](https://cloud.tencent.com/document/faq/436/56559)。阿里云 OSS 也提供对象 URL 和可选 CDN 回源；其文档注明加速范围包含中国大陆时域名需备案。[OSS CDN 加速](https://help.aliyun.com/en/oss/user-guide/cdn-acceleration)

发布时使用不可变版本路径（如 `office2pdf/v0.10.0/windows-x64.zip`），新版本换新路径，不覆盖旧对象；CDN 对已缓存对象可能继续返回旧内容，固定路径可避免依赖刷新旧缓存。[COS CDN 加速及缓存说明](https://cloud.tencent.com/document/product/436/18670)。应用首次需要预览时自动请求 manifest、显示下载进度，将内容流式写入本地临时文件；校验大小、SHA-256 与随包公钥验证的 manifest 签名后原子改名为版本缓存，失败可重试并保留已验证的旧版本。manifest 示意：

```json
{
  "version": "0.10.0",
  "platform": "windows-x64",
  "url": "https://downloads.example.cn/office2pdf/v0.10.0/windows-x64.zip",
  "size_bytes": "<发布资产字节数>",
  "sha256": "<发布资产 SHA-256>"
}
```

上述是可落地的分发结构，不代表已存在可用下载地址；本轮未创建云资源、上传发布或进行中国大陆网络测速，因此不能保证具体域名当前可达或 CDN 速度。

小规模分发可先用中国大陆区域 OSS/COS 提供的对象 HTTPS 直链，自定义下载域名和 CDN 后续按需要添加；若已有国内服务器，也可用它提供静态下载文件。发布流程先从 GitHub 获取并验证固定版本，在自有存储中保存相同字节的归档，再发布国内下载清单。运行时的清单和组件 URL 都应由该下载源提供，避免仍通过 GitHub API 获取地址。

已生成可审阅的 [双平台下载清单示例](../../build/research/office-preview-package-size-20261031/office-preview-manifest.example.json)，包含 Windows x64 与 macOS arm64 的固定 v0.10.0 归档字节数和 SHA-256。示例域名 `downloads.example.com` 不是已开通服务，文件也未签名；其中哈希只适用于原样镜像的官方归档，重新打包后必须重算。

当前应用的 [update.check](../../internal/platform/service.go#L81) 返回版本和 GitHub 发布页；[AutomaticUpdates](../../internal/productprofile/profile.go#L8) 为 false。Office 组件仍需新增专用下载、校验、缓存与版本管理能力。这里只下载引擎组件，资料文档继续在用户本机转换，不上传到下载服务。
