package com.snowball.silverwing.core

/** Application boundary for read-only inspection of Skills already installed on this machine. */
class LocalSkillCatalogApplicationService(
    private val catalog: LocalSkillCatalogService = LocalSkillCatalogService(),
) {
    fun list(): LocalSkillCatalog = catalog.list()

    fun files(directoryName: String): LocalSkillFileCatalog = catalog.files(directoryName)

    fun preview(directoryName: String, relativePath: String): String = catalog.preview(directoryName, relativePath)
}
