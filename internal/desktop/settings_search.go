package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

type settingsSearchEntry struct{ page, title, aliases string }

// Labels and aliases only: never index configuration values or credentials.
var settingsSearchEntries = []settingsSearchEntry{
	{"dev-tools", "全局默认开发工具", "默认IDE 默认编辑器"},
	{"dev-tools", "允许临时选择开发工具", "临时IDE 切换编辑器"},
	{"dev-tools", "IntelliJ IDEA", "IDEA 路径"},
	{"dev-tools", "WebStorm", "路径"},
	{"dev-tools", "PyCharm", "路径"},
	{"dev-tools", "Visual Studio Code", "VS Code vscode 路径"},
	{"dev-tools", "Android Studio", "路径"},
	{"dev-tools", "DevEco Studio", "路径"},
	{"proxy", "代理认证", "代理账号 代理密码 验证"},
	{"proxy", "不通过代理的地址", "代理例外 no_proxy"},
	{"proxy", "代理目标", "命令目标"},
	{"materials-sync", "同步周期（分钟）", "自动同步周期 时间 间隔"},
	{"materials-sync", "同步分支", "资料Git分支"},
	{"materials-sync", "Git 仓库地址", "资料仓库地址 repository"},
	{"tool-visibility", "JSON", "格式化 压缩 转义"},
	{"tool-visibility", "日期时间", "时间戳 转换"},
	{"tool-visibility", "文本对比", "text diff"},
	{"tool-visibility", "杀端口", "端口占用"},
	{"task-creation", "开启 AI 生成分支名和文件夹名", "AI命名开关"},
	{"task-creation", "拉取需求列表后自动预热命名", "AI预热"},
	{"tag", "全局 Tag 开关", "启用Tag"},
	{"tag", "允许在任务详情修改测试目标分支", "Tag目标编辑"},
	{"tag", "每任务最多保留 Tag 组", "历史保留数量"},
	{"cli-git", "Git 可执行文件", "gitExecutablePath 命令路径"},
	{"cli-codex", "Codex CLI 可执行文件", "codexExecutablePath 命令路径"},
	{"cli-meegle", "Meegle CLI 可执行文件", "meegleExecutablePath 命令路径"},
	{"cli-lark", "Lark CLI 可执行文件", "larkExecutablePath 命令路径 飞书机器人"},
	{"cli-lark", "Lark CLI 状态", "飞书 通知 机器人 身份"},
	{"cli-genbu", "Genbu CLI 可执行文件", "genbuExecutablePath 命令路径"},
	{"basic", "界面主题", "外观 浅色 深色 系统 theme"}, {"basic", "折叠工作空间导航", "最左侧 图标 宽度"},
	{"tool-visibility", "侧边栏展示", "导航 需求 生产发布 服务 入口"}, {"tool-visibility", "研发工具", "快捷工具 JSON 日期时间 时间戳 文本对比 diff 杀端口 端口占用"},
	{"task-area", "任务详情工具栏", "Git 路径 提交 推送 终端 Git 工具组 路径工具组"}, {"task-area", "工作区卡片工具栏", "Git 路径 打开文件夹 Git 工具组 路径工具组"}, {"task-area", "需求链接和需求编号复制", "复制图标 信息复制"},
	{"task-paths", "任务根目录", "任务目录 迁移 切换 taskRoot"}, {"task-creation", "任务打开工具", "创建 默认勾选 工作区"},
	{"task-creation", "默认分支名规则", "分支名 feature 编号 num ai"}, {"task-creation", "AI 命名", "模型 预热 自动命名 文件夹"},
	{"task-creation", "AI 命名模型", "AI命名 模型"},
	{"paths", "任务资料根目录", "资料目录 requirements 研发"}, {"paths", "任务资料子目录", "研发 子目录"}, {"paths", "默认展开目录层级", "折叠 目录树"},
	{"materials-sync", "资料 Git 自动同步", "资料同步 自动同步 仓库 分支 周期 立即同步 恢复"},
	{"agents", "全局与组说明", "AGENTS.md agent 指令 说明文档 markdown"}, {"instruction-templates", "任务说明模板", "新增 编辑 删除 模板 markdown"},
	{"git", "Git 状态", "环境 身份 user.name user.email"}, {"git", "分支写保护", "保护分支 master commit push"},
	{"tag", "Tag 设置", "构建 发布 测试目标 分支 保留数量 历史"},
	{"codex-plugins", "Codex 插件", "plugin marketplace 来源 安装 接管 卸载 预览"}, {"codex-mcp", "MCP 服务", "连接 授权 全局 当前任务 添加 删除 编辑"},
	{"skill-local", "本机 Skill", "本地 已安装 技能 浏览 SKILL.md"}, {"skill-sources", "Skill 来源", "技能 安装 来源 更新 接管 卸载"},
	{"dev-tools", "开发工具", "编辑器 IDE idea webstorm pycharm vscode android deveco 默认 临时 选择 检测 路径"},
	{"cli-git", "Git 状态", "Git 命令 路径 检测 可执行文件"}, {"cli-codex", "Codex CLI 状态", "codex 命令 路径 检测 可执行文件"},
	{"cli-meegle", "Meegle CLI 状态", "meego meegle 飞书 登录 账号 命令 路径 授权"}, {"cli-meegle", "Meegle 项目空间", "meego meegle 飞书 项目 key 短名 sprint 默认空间"},
	{"cli-meegle", "默认 Sprint 团队空间", "meego meegle 默认迭代 default sprint team space"},
	{"cli-genbu", "Genbu CLI 状态", "genbu 玄武 构建 发布 命令 路径"},
	{"proxy", "网络代理", "proxy http https socks5 协议 代理主机 端口 认证 账号 密码 例外 目标 测试 清除 应用更新 下载"},
	{"config-backup", "配置与备份", "配置源码 JSON 导入 导出 预览 恢复 删除 backup"},
	{"app-data", "数据目录", "应用数据目录 home 存储 打开 复制 路径"}, {"logs", "日志与诊断", "日志 错误 清理 复制 导出 diagnostics"},
	{"about", "关于与更新", "版本 下载 release github 仓库 更新"},
}

func settingsSearch(query string) []settingsSearchEntry {
	var results []settingsSearchEntry
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return results
	}
	for _, entry := range settingsSearchEntries {
		section, _ := settingsSectionForPage(entry.page)
		text := strings.ToLower(section.name + " " + entry.title + " " + entry.aliases)
		compact := strings.ReplaceAll(text, " ", "")
		matches := true
		for _, word := range words {
			if !strings.Contains(text, word) && !strings.Contains(compact, word) {
				matches = false
				break
			}
		}
		if matches {
			results = append(results, entry)
		}
	}
	return results
}

func (s *app) openSettingsSearchResult(entry settingsSearchEntry) {
	if s.navigateSettingsPage(entry.page, entry.title) {
		s.settingsUnit.state.settingsSearchQuery = ""
	}
}

type settingsRevealKey struct{}
type settingsRevealState struct {
	active, pending bool
	target          string
	epoch           uint64
	scroll          *ui.ScrollState
	viewport, last  ui.Rect
	stable, ready   bool
	interaction     uint64
}

func settingsReveal(c *ui.Context, e ui.Element, p palette, title string) ui.Element {
	state := ui.Local(c.Root(), settingsRevealKey{}, func() settingsRevealState { return settingsRevealState{} })
	if state.active && title != "" && state.target == title {
		e.Background(p.selected).Radius(4)
		if !state.pending || state.scroll == nil || !state.ready {
			return e
		}
		if state.scroll.InteractionVersion() != state.interaction {
			state.pending = false
			return e
		}
		bounds := e.Bounds()
		if bounds.H <= 0 || state.viewport.H <= 0 || !state.stable || bounds != state.last {
			state.last, state.stable = bounds, bounds.H > 0
			c.Invalidate()
			return e
		}
		state.scroll.Y = max(float32(0), state.scroll.Y+bounds.Y-state.viewport.Y-12)
		state.pending = false
		c.Invalidate()
	}
	return e
}

func (s *app) renderSettingsContent(c *ui.Context, p palette, reveal *settingsRevealState) {
	state := &s.settingsUnit.state
	if state.settingsContentScrolls == nil {
		state.settingsContentScrolls = make(map[string]*ui.ScrollState)
	}
	contentKey := s.settingsContentKey()
	scroll := state.settingsContentScrolls[contentKey]
	if scroll == nil {
		scroll = &ui.ScrollState{}
		state.settingsContentScrolls[contentKey] = scroll
	}
	reveal.ready = !s.busy && s.pageLocalReadReady()
	if state.settingsPage == "cli-tools" && s.toolsUnit.state.cliJobs != nil {
		if s.toolsUnit.state.cliJobs.requests["status:"+s.currentSettingsCLITool()] != nil {
			reveal.ready = false
		}
	}
	if reveal.scroll != scroll {
		reveal.scroll = scroll
		reveal.interaction = scroll.InteractionVersion()
	}
	bottom := float32(0)
	if reveal.target != "" {
		bottom = 96
	} // Leave the target clear of transient bottom notifications.
	content := ui.Scroll(c.Key("settings-content-"+contentKey)).Grow(1).MinHeight(0).Gap(14).Padding(0, 0, bottom, 0).TrackScroll(scroll)
	reveal.viewport = content.Bounds()
	content.HandleInput(func(event ui.InputEvent) bool {
		if event.Kind == ui.InputScroll || event.Kind == ui.InputPointerDown {
			state.settingsSearchPending, reveal.pending = false, false
		}
		return false
	})
	// Keyboard/text events belong to the focused child, not its Scroll ancestor.
	// Entering content transfers control to the user before any delayed reveal.
	if content.FocusWithin() {
		state.settingsSearchPending, reveal.pending = false, false
	}
	if reveal.pending && settingsSearchWholePage(state.settingsPage) {
		scroll.Y = 0
		reveal.pending = false
	}
	content.Children(func() { s.settingsPageContent(c, p) })
}

func settingsSearchWholePage(page string) bool {
	switch page {
	case "codex-plugins", "codex-mcp", "instruction-templates", "logs", "about", "app-data":
		return true
	}
	return false
}
