package application

import "strings"

// ActionDescriptor owns application operation behavior used by the desktop.
// Page-specific leases and response identity checks remain with their owner.
// Unknown extension/CLI operations retain the conservative default policy.
type ActionDescriptor struct {
	Kind, Label             string
	Known                   bool
	TaskMenuScoped          bool
	ReloadSnapshot          bool
	RefreshOnError          bool
	QuietSuccess            bool
	SettingsDedicatedResult bool
	DedicatedResult         bool
	Cancellable             bool
}

func DescribeAction(kind string) ActionDescriptor {
	descriptor, known := actionDescriptors[kind]
	if !known {
		descriptor = ActionDescriptor{Kind: kind, Label: kind}
	}
	descriptor.DedicatedResult = descriptor.DedicatedResult || strings.HasPrefix(kind, "instructions.")
	descriptor.SettingsDedicatedResult = strings.HasPrefix(kind, "config.") || strings.HasPrefix(kind, "migration.") ||
		strings.HasPrefix(kind, "instructions.templates.") || strings.HasPrefix(kind, "skills.") ||
		strings.HasPrefix(kind, "codex.extensions/") || strings.HasPrefix(kind, "codex.ai/") ||
		strings.HasPrefix(kind, "cli.") || strings.HasPrefix(kind, "logs.") || strings.HasPrefix(kind, "development-tools/") || strings.HasPrefix(kind, "git.environment") ||
		kind == "terminal.resolve" || kind == "diagnostics.export" || kind == "proxy.test" || kind == "git.version" || kind == "update.check"
	return descriptor
}

// Each operation has exactly one policy record. Adding a task-menu operation
// therefore does not require independent scope, refresh and label lists.
var actionDescriptors = map[string]ActionDescriptor{
	"task.requirement-link":                        {Kind: "task.requirement-link", Label: "关联需求", Known: true, TaskMenuScoped: true, ReloadSnapshot: true},
	"requirements.copy-document":                   {Kind: "requirements.copy-document", Label: "requirements.copy-document", Known: true, DedicatedResult: true},
	"requirements.copy-comments":                   {Kind: "requirements.copy-comments", Label: "requirements.copy-comments", Known: true, DedicatedResult: true},
	"task.create-preview":                          {Kind: "task.create-preview", Label: "task.create-preview", Known: true},
	"cli.auth.logout":                              {Kind: "cli.auth.logout", Label: "cli.auth.logout", Known: true, Cancellable: true},
	"cli.auth.start":                               {Kind: "cli.auth.start", Label: "cli.auth.start", Known: true, Cancellable: true},
	"codex.ai/mcp/authenticate":                    {Kind: "codex.ai/mcp/authenticate", Label: "codex.ai/mcp/authenticate", Known: true, Cancellable: true},
	"codex.extensions":                             {Kind: "codex.extensions", Label: "读取 Codex 扩展", Known: true},
	"codex.extensions/marketplaces/add-source":     {Kind: "codex.extensions/marketplaces/add-source", Label: "codex.extensions/marketplaces/add-source", Known: true, RefreshOnError: true, Cancellable: true},
	"codex.extensions/marketplaces/refresh-source": {Kind: "codex.extensions/marketplaces/refresh-source", Label: "codex.extensions/marketplaces/refresh-source", Known: true, Cancellable: true},
	"codex.extensions/marketplaces/remove-source":  {Kind: "codex.extensions/marketplaces/remove-source", Label: "codex.extensions/marketplaces/remove-source", Known: true, Cancellable: true},
	"codex.extensions/mcp/add":                     {Kind: "codex.extensions/mcp/add", Label: "codex.extensions/mcp/add", Known: true, Cancellable: true},
	"codex.extensions/mcp/check":                   {Kind: "codex.extensions/mcp/check", Label: "codex.extensions/mcp/check", Known: true, Cancellable: true},
	"codex.extensions/mcp/edit":                    {Kind: "codex.extensions/mcp/edit", Label: "codex.extensions/mcp/edit", Known: true, Cancellable: true},
	"codex.extensions/mcp/remove":                  {Kind: "codex.extensions/mcp/remove", Label: "codex.extensions/mcp/remove", Known: true, Cancellable: true},
	"codex.extensions/mcp/set":                     {Kind: "codex.extensions/mcp/set", Label: "codex.extensions/mcp/set", Known: true, Cancellable: true},
	"codex.extensions/mcp/status":                  {Kind: "codex.extensions/mcp/status", Label: "codex.extensions/mcp/status", Known: true, Cancellable: true},
	"codex.extensions/plugin/install":              {Kind: "codex.extensions/plugin/install", Label: "codex.extensions/plugin/install", Known: true, Cancellable: true},
	"codex.extensions/plugin/uninstall":            {Kind: "codex.extensions/plugin/uninstall", Label: "codex.extensions/plugin/uninstall", Known: true, Cancellable: true},
	"codex.extensions/snapshot":                    {Kind: "codex.extensions/snapshot", Label: "读取 Codex 扩展", Known: true},
	"codex.run":                                    {Kind: "codex.run", Label: "在 Codex 中打开", Known: true, TaskMenuScoped: true, ReloadSnapshot: true, RefreshOnError: true},
	"config.fields.save":                           {Kind: "config.fields.save", Label: "保存设置", Known: true, ReloadSnapshot: true},
	"config.import":                                {Kind: "config.import", Label: "导入配置", Known: true, ReloadSnapshot: true},
	"config.import-preview":                        {Kind: "config.import-preview", Label: "预览配置", Known: true},
	"config.materials-root.save":                   {Kind: "config.materials-root.save", Label: "config.materials-root.save", Known: true, ReloadSnapshot: true},
	"config.restore-backup":                        {Kind: "config.restore-backup", Label: "config.restore-backup", Known: true, ReloadSnapshot: true},
	"config.save":                                  {Kind: "config.save", Label: "保存配置", Known: true, ReloadSnapshot: true},
	"config.task-root.save":                        {Kind: "config.task-root.save", Label: "切换任务目录", Known: true, ReloadSnapshot: true},
	"config.view":                                  {Kind: "config.view", Label: "config.view", Known: true, DedicatedResult: true},
	"development-tools/detect":                     {Kind: "development-tools/detect", Label: "检测开发工具", Known: true, ReloadSnapshot: true},
	"development-tools/snapshot":                   {Kind: "development-tools/snapshot", Label: "读取开发工具配置", Known: true, ReloadSnapshot: true},
	"diagnostics.export":                           {Kind: "diagnostics.export", Label: "导出诊断信息", Known: true, Cancellable: true},
	"document.copy":                                {Kind: "document.copy", Label: "复制文档", Known: true},
	"document.create":                              {Kind: "document.create", Label: "新建文档", Known: true},
	"document.list":                                {Kind: "document.list", Label: "读取资料目录", Known: true, QuietSuccess: true},
	"document.mkdir":                               {Kind: "document.mkdir", Label: "创建目录", Known: true},
	"document.open":                                {Kind: "document.open", Label: "打开文档", Known: true},
	"document.read":                                {Kind: "document.read", Label: "读取文档", Known: true, QuietSuccess: true},
	"document.rename":                              {Kind: "document.rename", Label: "重命名文档", Known: true},
	"document.reveal":                              {Kind: "document.reveal", Label: "显示文档位置", Known: true},
	"document.save":                                {Kind: "document.save", Label: "保存文档", Known: true},
	"document.trash":                               {Kind: "document.trash", Label: "移至回收站", Known: true},
	"instructions.read":                            {Kind: "instructions.read", Label: "读取任务说明", Known: true, QuietSuccess: true, DedicatedResult: true},
	"instructions.save":                            {Kind: "instructions.save", Label: "保存任务说明", Known: true, ReloadSnapshot: true, DedicatedResult: true},
	"meegle.projects":                              {Kind: "meegle.projects", Label: "meegle.projects", Known: true, DedicatedResult: true},
	"migration.apply":                              {Kind: "migration.apply", Label: "migration.apply", Known: true, ReloadSnapshot: true},
	"migration.recover":                            {Kind: "migration.recover", Label: "migration.recover", Known: true, ReloadSnapshot: true},
	"repository.add":                               {Kind: "repository.add", Label: "添加仓库", Known: true, ReloadSnapshot: true, Cancellable: true},
	"repository.remove":                            {Kind: "repository.remove", Label: "移除仓库", Known: true, ReloadSnapshot: true},
	"requirements.attachment":                      {Kind: "requirements.attachment", Label: "requirements.attachment", Known: true, DedicatedResult: true},
	"requirements.comment":                         {Kind: "requirements.comment", Label: "发表评论", Known: true, ReloadSnapshot: true},
	"requirements.comments":                        {Kind: "requirements.comments", Label: "读取需求评论", Known: true, DedicatedResult: true},
	"requirements.list":                            {Kind: "requirements.list", Label: "读取需求列表", Known: true},
	"requirements.metadata":                        {Kind: "requirements.metadata", Label: "requirements.metadata", Known: true},
	"requirements.read":                            {Kind: "requirements.read", Label: "读取需求详情", Known: true, DedicatedResult: true},
	"requirements.refresh":                         {Kind: "requirements.refresh", Label: "刷新需求", Known: true, ReloadSnapshot: true},
	"service.add-repositories":                     {Kind: "service.add-repositories", Label: "添加仓库", Known: true, ReloadSnapshot: true, Cancellable: true},
	"service.module-remove":                        {Kind: "service.module-remove", Label: "移除模块", Known: true, ReloadSnapshot: true},
	"service.module-save":                          {Kind: "service.module-save", Label: "保存模块", Known: true, ReloadSnapshot: true},
	"service.remove":                               {Kind: "service.remove", Label: "移除服务", Known: true, ReloadSnapshot: true},
	"service.save":                                 {Kind: "service.save", Label: "保存服务", Known: true, ReloadSnapshot: true},
	"skills.install":                               {Kind: "skills.install", Label: "安装 Skill", Known: true, ReloadSnapshot: true, Cancellable: true},
	"skills.refresh":                               {Kind: "skills.refresh", Label: "刷新 Skills", Known: true, ReloadSnapshot: true},
	"skills.remove":                                {Kind: "skills.remove", Label: "移除 Skill", Known: true, ReloadSnapshot: true, Cancellable: true},
	"skills.sources/catalog":                       {Kind: "skills.sources/catalog", Label: "skills.sources/catalog", Known: true, Cancellable: true},
	"skills.sources/remove":                        {Kind: "skills.sources/remove", Label: "skills.sources/remove", Known: true, Cancellable: true},
	"skills.sources/save":                          {Kind: "skills.sources/save", Label: "skills.sources/save", Known: true, RefreshOnError: true, Cancellable: true},
	"skills.update":                                {Kind: "skills.update", Label: "skills.update", Known: true, Cancellable: true},
	"system.ide":                                   {Kind: "system.ide", Label: "启动开发工具", Known: true},
	"system.open":                                  {Kind: "system.open", Label: "打开路径", Known: true},
	"system.reveal":                                {Kind: "system.reveal", Label: "显示路径", Known: true},
	"system.terminal":                              {Kind: "system.terminal", Label: "打开终端", Known: true},
	"tag.build":                                    {Kind: "tag.build", Label: "构建 Tag", Known: true, ReloadSnapshot: true},
	"tag.genbu.refresh":                            {Kind: "tag.genbu.refresh", Label: "刷新 Genbu 状态", Known: true, QuietSuccess: true, DedicatedResult: true},
	"tag.history":                                  {Kind: "tag.history", Label: "读取 Tag 历史", Known: true, DedicatedResult: true},
	"tag.history.clear":                            {Kind: "tag.history.clear", Label: "清空Tag历史", Known: true, ReloadSnapshot: true, DedicatedResult: true},
	"tag.history.delete":                           {Kind: "tag.history.delete", Label: "删除所选Tag历史", Known: true, ReloadSnapshot: true, DedicatedResult: true},
	"tag.history.refresh":                          {Kind: "tag.history.refresh", Label: "刷新 Tag 历史和 Genbu", Known: true, DedicatedResult: true},
	"tag.retry":                                    {Kind: "tag.retry", Label: "重试 Tag 操作", Known: true, ReloadSnapshot: true},
	"tag.status":                                   {Kind: "tag.status", Label: "查询 Tag 状态", Known: true, QuietSuccess: true, DedicatedResult: true},
	"tag.target.update":                            {Kind: "tag.target.update", Label: "保存测试目标分支", Known: true, ReloadSnapshot: true},
	"tag.workspace-check":                          {Kind: "tag.workspace-check", Label: "检查工作区", Known: true, QuietSuccess: true, DedicatedResult: true},
	"task.archive":                                 {Kind: "task.archive", Label: "归档任务", Known: true, TaskMenuScoped: true, ReloadSnapshot: true},
	"task.create":                                  {Kind: "task.create", Label: "创建任务", Known: true, ReloadSnapshot: true, Cancellable: true},
	"task.delete":                                  {Kind: "task.delete", Label: "删除任务", Known: true, TaskMenuScoped: true, ReloadSnapshot: true, RefreshOnError: true},
	"task.delete-risks":                            {Kind: "task.delete-risks", Label: "检查删除风险", Known: true},
	"task.materials-retry":                         {Kind: "task.materials-retry", Label: "关联任务资料", Known: true, TaskMenuScoped: true, ReloadSnapshot: true, Cancellable: true},
	"task.modules-add":                             {Kind: "task.modules-add", Label: "添加模块", Known: true, ReloadSnapshot: true, Cancellable: true},
	"task.modules-add-preview":                     {Kind: "task.modules-add-preview", Label: "预检新增模块", Known: true},
	"task.refresh":                                 {Kind: "task.refresh", Label: "刷新任务", Known: true, ReloadSnapshot: true, Cancellable: true},
	"task.restore":                                 {Kind: "task.restore", Label: "恢复任务", Known: true, TaskMenuScoped: true, ReloadSnapshot: true},
	"task.services-add":                            {Kind: "task.services-add", Label: "添加服务", Known: true, ReloadSnapshot: true, Cancellable: true},
	"task.services-add-preview":                    {Kind: "task.services-add-preview", Label: "预检新增服务", Known: true},
	"task.services-retry":                          {Kind: "task.services-retry", Label: "重试失败服务", Known: true, ReloadSnapshot: true, Cancellable: true},
	"task.tools-launch":                            {Kind: "task.tools-launch", Label: "打开任务工具", Known: true, TaskMenuScoped: true, ReloadSnapshot: true, RefreshOnError: true},
	"task.tools-retry":                             {Kind: "task.tools-retry", Label: "重试工作区工具", Known: true, ReloadSnapshot: true},
	"tool.diff":                                    {Kind: "tool.diff", Label: "比较文本", Known: true},
	"tool.json":                                    {Kind: "tool.json", Label: "处理 JSON", Known: true},
	"tool.port-stop":                               {Kind: "tool.port-stop", Label: "结束端口进程", Known: true},
	"tool.ports":                                   {Kind: "tool.ports", Label: "查询端口", Known: true},
	"tool.time":                                    {Kind: "tool.time", Label: "转换时间", Known: true},
	"workspace.add":                                {Kind: "workspace.add", Label: "添加工作区", Known: true, ReloadSnapshot: true},
	"workspace.batch":                              {Kind: "workspace.batch", Label: "批量 Git 操作", Known: true, ReloadSnapshot: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.bootstrap-clear":                    {Kind: "workspace.bootstrap-clear", Label: "清除 Bootstrap 警告", Known: true, ReloadSnapshot: true, DedicatedResult: true},
	"workspace.bootstrap-rerun":                    {Kind: "workspace.bootstrap-rerun", Label: "重新执行 Bootstrap", Known: true, ReloadSnapshot: true, DedicatedResult: true},
	"workspace.command":                            {Kind: "workspace.command", Label: "运行命令", Known: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.commit":                             {Kind: "workspace.commit", Label: "提交更改", Known: true, ReloadSnapshot: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.commit-push":                        {Kind: "workspace.commit-push", Label: "提交并推送", Known: true, ReloadSnapshot: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.diff-files":                         {Kind: "workspace.diff-files", Label: "workspace.diff-files", Known: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.diff-preview":                       {Kind: "workspace.diff-preview", Label: "workspace.diff-preview", Known: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.fetch":                              {Kind: "workspace.fetch", Label: "获取远程更新", Known: true, ReloadSnapshot: true},
	"workspace.file-preview":                       {Kind: "workspace.file-preview", Label: "workspace.file-preview", Known: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.history":                            {Kind: "workspace.history", Label: "workspace.history", Known: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.merge":                              {Kind: "workspace.merge", Label: "合并主分支", Known: true, ReloadSnapshot: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.merge-batch":                        {Kind: "workspace.merge-batch", Label: "批量合并主分支", Known: true, ReloadSnapshot: true, QuietSuccess: true, DedicatedResult: true, Cancellable: true},
	"workspace.preview":                            {Kind: "workspace.preview", Label: "读取提交预览", Known: true},
	"workspace.previews":                           {Kind: "workspace.previews", Label: "读取 Git 变更预览", Known: true},
	"workspace.push":                               {Kind: "workspace.push", Label: "推送更改", Known: true, ReloadSnapshot: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.remove":                             {Kind: "workspace.remove", Label: "移除工作区", Known: true, ReloadSnapshot: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.remove-preview":                     {Kind: "workspace.remove-preview", Label: "workspace.remove-preview", Known: true, QuietSuccess: true, DedicatedResult: true},
	"workspace.repair":                             {Kind: "workspace.repair", Label: "修复工作区", Known: true, ReloadSnapshot: true, QuietSuccess: true, DedicatedResult: true, Cancellable: true},
	"workspace.repair-preview":                     {Kind: "workspace.repair-preview", Label: "检查工作区修复方案", Known: true, QuietSuccess: true, DedicatedResult: true, Cancellable: true},
	"workspace.status":                             {Kind: "workspace.status", Label: "检查工作区", Known: true, QuietSuccess: true, DedicatedResult: true},
}
