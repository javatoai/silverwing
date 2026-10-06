package com.snowball.silverwing.desktop

private const val MATERIALS_STAGING_UUID = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"
private val materialsStagingName = Regex("\\.silverwing-(?:import-$MATERIALS_STAGING_UUID\\.tmp|rename-$MATERIALS_STAGING_UUID)", RegexOption.IGNORE_CASE)

/** Only our exact temporary names are hidden; ordinary dotfiles remain materials. */
internal fun isMaterialsStagingName(name: String?): Boolean = name?.let(materialsStagingName::matches) == true
