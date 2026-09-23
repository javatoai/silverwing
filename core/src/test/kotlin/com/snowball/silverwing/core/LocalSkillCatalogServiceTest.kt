package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class LocalSkillCatalogServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `discovers installed Skills and safely previews markdown references and scripts`() {
        val userHome = temporary.resolve("user")
        val root = userHome.resolve(".agents/skills")
        skill(root.resolve("release-check"), "release-check", "Check a release")
        Files.createDirectories(root.resolve("release-check/references"))
        Files.writeString(root.resolve("release-check/references/checklist.md"), "# Checklist\n")
        Files.createDirectories(root.resolve("release-check/scripts"))
        Files.writeString(root.resolve("release-check/scripts/check.ps1"), "Write-Output 'ok'\n")
        Files.createDirectories(root.resolve("release-check/.git"))
        Files.writeString(root.resolve("release-check/.git/config"), "not part of the Skill preview")
        Files.createDirectories(root.resolve("not-a-skill"))
        Files.writeString(root.resolve("not-a-skill/readme.md"), "ignored")
        skill(root.resolve("deploy"), "deploy", "Deploy a service")

        val service = LocalSkillCatalogService { userHome }

        val catalog = service.list()
        assertEquals(root.toAbsolutePath().normalize().toString(), catalog.root)
        assertEquals(listOf("deploy", "release-check"), catalog.skills.map(LocalSkillCatalogItem::directoryName))
        assertEquals("Check a release", catalog.skills.single { it.directoryName == "release-check" }.description)

        val files = service.files("release-check")
        assertEquals(listOf("SKILL.md", "references/checklist.md", "scripts/check.ps1"), files.files.map(LocalSkillFileEntry::relativePath))
        assertFalse(files.truncated)
        assertTrue(files.files.single { it.relativePath == "references/checklist.md" }.markdown)
        assertFalse(files.files.single { it.relativePath == "scripts/check.ps1" }.markdown)
        assertEquals("# Checklist\n", service.preview("release-check", "references/checklist.md"))
        assertEquals("Write-Output 'ok'\n", service.preview("release-check", "scripts/check.ps1"))
        assertFailsWith<IllegalArgumentException> { service.preview("release-check", "../outside.txt") }
    }

    @Test
    fun `missing root is empty and binary or oversized files are rejected without arbitrary reads`() {
        val userHome = temporary.resolve("empty-user")
        val service = LocalSkillCatalogService { userHome }

        assertTrue(service.list().skills.isEmpty())

        val root = userHome.resolve(".agents/skills")
        skill(root.resolve("inspect"), "inspect", "Inspect files")
        Files.write(root.resolve("inspect/scripts.bin"), byteArrayOf(0x01, 0x00, 0x02))
        Files.writeString(root.resolve("inspect/large.txt"), "x".repeat(512 * 1024 + 1))

        assertFailsWith<IllegalArgumentException> { service.preview("inspect", "scripts.bin") }
        assertFailsWith<IllegalArgumentException> { service.preview("inspect", "large.txt") }
        assertFailsWith<IllegalArgumentException> { service.files("..") }
    }

    private fun skill(directory: Path, name: String, description: String) {
        Files.createDirectories(directory)
        Files.writeString(directory.resolve("SKILL.md"), "---\nname: $name\ndescription: $description\n---\n# $name\n")
    }
}
