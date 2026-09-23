package com.snowball.silverwing.core

/**
 * Owns global defaults applied when a new task form opens.  Project groups
 * remain responsible only for their service and Tag membership.
 */
class TaskCreationDefaultsService(
    private val configurations: ConfigurationRepository = ConfigStore(),
) {
    fun updateBranchPrefix(rawPrefix: String): AppConfig = configurations.update { config ->
        config.copy(defaultBranchPrefix = rawPrefix.trim())
    }

    fun updateWorkspaceToolIds(rawToolIds: List<String>): AppConfig = configurations.update { config ->
        config.copy(
            defaultWorkspaceToolIds = rawToolIds
                .map(String::trim)
                .filter(String::isNotEmpty)
                .distinct(),
        )
    }
}
