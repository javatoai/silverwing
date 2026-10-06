package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.util.UUID
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.encodeToString

internal data class ServiceEditorError(val section: String, val itemId: String? = null, val field: String, val message: String)
internal class ServiceEditorValidationException(val issue: ServiceEditorError) : IllegalArgumentException(issue.message)

internal enum class BootstrapEditorPage(val section: String, val title: String) {
    COPIES("copies", "复制规则"), COMMANDS("commands", "初始化命令")
}

internal fun parseBootstrapEditorJson(json: Json, text: String, page: BootstrapEditorPage): BootstrapConfig =
    checkEditor(page.section, field = "json") {
        val config = try {
            when (page) {
                BootstrapEditorPage.COPIES -> BootstrapConfig(copyRules = json.decodeFromString<List<BootstrapCopyRule>>(text))
                BootstrapEditorPage.COMMANDS -> BootstrapConfig(commands = json.decodeFromString<List<BootstrapCommand>>(text))
            }
        }
        catch (error: SerializationException) {
            val offset = Regex("offset (\\d+)").find(error.message.orEmpty())?.groupValues?.get(1)
            throw IllegalArgumentException("JSON 格式错误" + (offset?.let { "（位置 " + it + "）" } ?: "") + "，请检查字段、引号、逗号和括号。")
        }
        config.validated()
    }

internal data class BootstrapCopyDraft(val key: String = UUID.randomUUID().toString(), val rule: BootstrapCopyRule = BootstrapCopyRule("", ""))
internal data class BootstrapCommandDraft(
    val key: String = UUID.randomUUID().toString(),
    val command: BootstrapCommand = BootstrapCommand(name = "初始化", executable = ""),
    val argumentsText: String = command.arguments.joinToString("\n"),
    val timeoutText: String = command.timeoutSeconds.toString(),
)

/** 界面标识与原始输入只存在草稿中，不进入配置 schema。 */
internal data class BootstrapEditorDraft(
    val copies: List<BootstrapCopyDraft> = emptyList(),
    val commands: List<BootstrapCommandDraft> = emptyList(),
) {
    fun copyRules(): List<BootstrapCopyRule> {
        copies.forEach { entry ->
            listOf("source" to entry.rule.source, "target" to entry.rule.target).forEach { (field, value) ->
                val rule = if (field == "source") BootstrapCopyRule(value, "placeholder") else BootstrapCopyRule("placeholder", value)
                checkEditor("copies", entry.key, field) { BootstrapConfig(copyRules = listOf(rule)).validated() }
            }
        }
        return copies.map { it.rule }
    }

    fun initializationCommands(): List<BootstrapCommand> = commands.map { entry ->
            fun requireField(field: String, condition: Boolean, message: String) = checkEditor("commands", entry.key, field) { require(condition) { message } }
            requireField("name", entry.command.name.isNotBlank(), "请填写命令名称")
            requireField("executable", entry.command.executable.isNotBlank(), "请填写可执行程序")
            requireField("workingDirectory", entry.command.workingDirectory.isNotBlank(), "请填写工作目录")
            val timeout = entry.timeoutText.toLongOrNull()
            requireField("timeout", timeout != null && timeout > 0, "超时必须是大于 0 的整数")
            val command = entry.command.copy(arguments = entry.argumentsText.lines().filter(String::isNotBlank), timeoutSeconds = timeout!!)
            checkEditor("commands", entry.key, "workingDirectory") { BootstrapConfig(commands = listOf(command)).validated() }
            command
        }

    fun toConfig(): BootstrapConfig = BootstrapConfig(copyRules(), initializationCommands()).validated()
}

internal data class BootstrapJsonEditor(val mode: String = "form", val text: String)

/** 两页各自转换和保留原文；只有最终保存才合并，未完成的另一页不会阻止编辑。 */
internal data class BootstrapEditorSession(
    val draft: BootstrapEditorDraft,
    val copies: BootstrapJsonEditor,
    val commands: BootstrapJsonEditor,
) {
    fun editor(page: BootstrapEditorPage) = if (page == BootstrapEditorPage.COPIES) copies else commands
    fun withEditor(page: BootstrapEditorPage, editor: BootstrapJsonEditor): BootstrapEditorSession =
        if (page == BootstrapEditorPage.COPIES) copy(copies = editor) else copy(commands = editor)

    fun switchMode(json: Json, page: BootstrapEditorPage, mode: String): BootstrapEditorSession {
        val current = editor(page)
        if (current.mode == mode) return this
        if (mode == "json") {
            val text = when (page) {
                BootstrapEditorPage.COPIES -> json.encodeToString(draft.copyRules())
                BootstrapEditorPage.COMMANDS -> json.encodeToString(draft.initializationCommands())
            }
            return withEditor(page, BootstrapJsonEditor(mode, text))
        }
        val parsed = parseBootstrapEditorJson(json, current.text, page)
        val reconciled = when (page) {
            BootstrapEditorPage.COPIES -> draft.copy(copies = draft.reconcile(parsed).copies)
            BootstrapEditorPage.COMMANDS -> draft.copy(commands = draft.reconcile(parsed).commands)
        }
        return copy(draft = reconciled).withEditor(page, current.copy(mode = mode))
    }

    fun toConfig(json: Json): BootstrapConfig {
        val rules = if (copies.mode == "json") parseBootstrapEditorJson(json, copies.text, BootstrapEditorPage.COPIES).copyRules else draft.copyRules()
        val initialization = if (commands.mode == "json") parseBootstrapEditorJson(json, commands.text, BootstrapEditorPage.COMMANDS).commands else draft.initializationCommands()
        return BootstrapConfig(rules, initialization).validated()
    }
}

internal fun BootstrapConfig.toEditorSession(json: Json) = BootstrapEditorSession(
    toEditorDraft(), BootstrapJsonEditor(text = json.encodeToString(copyRules)), BootstrapJsonEditor(text = json.encodeToString(commands)),
)

internal fun BootstrapConfig.toEditorDraft() = BootstrapEditorDraft(copyRules.map { BootstrapCopyDraft(rule = it) }, commands.map { BootstrapCommandDraft(command = it) })

internal fun BootstrapEditorDraft.reconcile(config: BootstrapConfig): BootstrapEditorDraft {
    val copyCandidates = copies.toMutableList()
    val commandCandidates = commands.toMutableList()
    return BootstrapEditorDraft(config.copyRules.mapIndexed { index, rule ->
        val previous = copyCandidates.firstOrNull { it.rule == rule } ?: copies.getOrNull(index)?.takeIf { it in copyCandidates }
        copyCandidates.remove(previous)
        BootstrapCopyDraft(key = previous?.key ?: UUID.randomUUID().toString(), rule = rule)
    }, config.commands.mapIndexed { index, command ->
        val previous = commandCandidates.firstOrNull { it.command == command } ?: commands.getOrNull(index)?.takeIf { it in commandCandidates }
        commandCandidates.remove(previous)
        BootstrapCommandDraft(key = previous?.key ?: UUID.randomUUID().toString(), command = command)
    })
}

internal inline fun <T> checkEditor(section: String, itemId: String? = null, field: String, block: () -> T): T =
    try { block() } catch (error: Exception) {
        throw ServiceEditorValidationException(ServiceEditorError(section, itemId, field, error.message ?: "请检查此项配置"))
    }

internal fun validateModuleDrafts(drafts: List<ServiceModuleEditorDraft>): List<ServiceModuleConfig> {
    val modules = drafts.map { draft ->
        checkEditor("modules", draft.id, "masterBranch") { draft.masterBranch?.let { RemoteBranchRef.parse(it.trim()) } }
        if (draft.tagMode != TagBuildMode.CURRENT_BRANCH) checkEditor("modules", draft.id, "tagTarget") { draft.tagTargetRef?.let { RemoteBranchRef.parse(it.trim()) } }
        draft.customCommands.forEach { command ->
            checkEditor("modules", command.id, "name") { require(command.name.isNotBlank()) { "请填写命令名称" } }
            checkEditor("modules", command.id, "executable") { require(command.executable.isNotBlank()) { "请填写可执行程序" } }
            checkEditor("modules", command.id, "timeout") { require((command.timeoutSeconds.toLongOrNull() ?: 0) > 0) { "超时必须是大于 0 的整数" } }
            checkEditor("modules", command.id, "workingDirectory") { command.toConfig().validateWorkspaceCommand() }
        }
        val module = checkEditor("modules", draft.id, "name") { draft.toConfig() }
        checkEditor("modules", draft.id, "name") { StandardWorktreeModuleNaming.requireValid(listOf(module)) }
        module
    }
    modules.forEachIndexed { index, module ->
        checkEditor("modules", module.id, "name") { validateServiceWorkspaceModules(modules.take(index + 1)) }
    }
    return modules
}

internal data class ServiceEditorBounds(val width: Float, val maxHeight: Float, val compact: Boolean) {
    val height: Float get() = maxHeight.coerceAtMost(720f)
}
internal fun serviceEditorBounds(windowWidth: Float, windowHeight: Float): ServiceEditorBounds {
    val width = (windowWidth - 40).coerceAtLeast(1f).coerceAtMost(1000f)
    return ServiceEditorBounds(width, (windowHeight - 48).coerceAtLeast(1f), width < 800)
}
