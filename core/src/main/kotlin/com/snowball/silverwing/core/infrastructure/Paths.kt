package com.snowball.silverwing.core

import java.nio.file.Path
import kotlin.io.path.Path

data class ApplicationPaths(
    val home: Path,
) {
    val config: Path = home.resolve("config")
    val logs: Path = home.resolve("logs")
    val backups: Path = home.resolve("backups")
    val diagnostics: Path = home.resolve("diagnostics")
    val locks: Path = home.resolve("locks")
    val temp: Path = home.resolve("temp")
    /** Local-only Codex extension caches, source checkouts, ownership records, and recoverable backups. */
    val codex: Path = home.resolve("codex")
    val agents: Path = home.resolve("agents")
    val tasks: Path = home.resolve("tasks")
    val globalAgents: Path = agents.resolve("global").resolve("AGENTS.md")
    val agentTaskTemplates: Path = agents.resolve("task-templates.json")

    fun groupAgents(groupId: String): Path {
        require(groupId.matches(Regex("[A-Za-z0-9._-]+"))) { "组 ID 不能用于文件路径：$groupId" }
        return agents.resolve("groups").resolve(groupId).resolve("AGENTS.md")
    }

    companion object {
        fun systemDefault(
            userHome: String = System.getProperty("user.home"),
        ): ApplicationPaths = ApplicationPaths(Path(userHome).resolve("silverwing"))
    }
}
