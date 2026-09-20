package com.snowball.silverwing.desktop

import java.nio.file.Files
import java.nio.file.Path
import java.util.Comparator
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class TagSkillInstallationServiceTest {
    @Test
    fun `install creates the user local silverwing skill directory`() {
        val root = Files.createTempDirectory("silverwing-tag-skill-install")
        try {
            val home = root.resolve("home")
            val source = skillSource(root.resolve("bundle"))
            val service = PackagedTagSkillInstallationService(source = { source }, userHome = { home })

            val before = service.inspect()
            assertTrue(before.bundledPayloadAvailable)
            assertFalse(before.installed)
            assertEquals(home.resolve(".agents/skills/silverwing"), before.destination)

            val installed = service.install()

            assertTrue(installed.installed)
            assertTrue(Files.isRegularFile(home.resolve(".agents/skills/silverwing/SKILL.md")))
            assertTrue(Files.isRegularFile(home.resolve(".agents/skills/silverwing/references/tag-builds.md")))
            assertContains(Files.readString(home.resolve(".agents/skills/silverwing/SKILL.md")), "name: silverwing")
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `install deletes a pre-existing target before directly copying the packaged skill`() {
        val root = Files.createTempDirectory("silverwing-tag-skill-overwrite")
        try {
            val home = root.resolve("home")
            val destination = home.resolve(".agents/skills/silverwing")
            Files.createDirectories(destination.resolve("references"))
            Files.writeString(destination.resolve("SKILL.md"), "custom skill")
            Files.writeString(destination.resolve("references/obsolete.md"), "obsolete")
            val source = skillSource(root.resolve("bundle"))
            val service = PackagedTagSkillInstallationService(source = { source }, userHome = { home })

            assertTrue(service.inspect().destinationOccupied)
            val installed = service.install()

            assertTrue(installed.installed)
            assertFalse(Files.exists(destination.resolve("references/obsolete.md")))
            assertContains(Files.readString(destination.resolve("SKILL.md")), "Tag-only content")
            Files.list(checkNotNull(destination.parent)).use { children ->
                assertFalse(children.anyMatch { child ->
                    child.fileName.toString().startsWith(".silverwing-backup-") ||
                        child.fileName.toString().startsWith(".silverwing-staging-")
                })
            }
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `missing or incomplete bundled skill does not replace an existing destination`() {
        val root = Files.createTempDirectory("silverwing-tag-skill-missing")
        try {
            val home = root.resolve("home")
            val destination = home.resolve(".agents/skills/silverwing")
            Files.createDirectories(destination)
            Files.writeString(destination.resolve("SKILL.md"), "custom skill")
            val incomplete = root.resolve("incomplete")
            Files.createDirectories(incomplete)
            Files.writeString(incomplete.resolve("SKILL.md"), "---\nname: other\n---")
            val service = PackagedTagSkillInstallationService(source = { incomplete }, userHome = { home })

            assertFalse(service.inspect().bundledPayloadAvailable)
            assertFailsWith<IllegalArgumentException> { service.install() }
            assertEquals("custom skill", Files.readString(destination.resolve("SKILL.md")))
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `uninstall removes only the target skill directory`() {
        val root = Files.createTempDirectory("silverwing-tag-skill-uninstall")
        try {
            val home = root.resolve("home")
            val source = skillSource(root.resolve("bundle"))
            val service = PackagedTagSkillInstallationService(source = { source }, userHome = { home })
            val sibling = home.resolve(".agents/skills/another-skill")
            Files.createDirectories(sibling)
            Files.writeString(sibling.resolve("SKILL.md"), "another skill")

            service.install()
            val uninstalled = service.uninstall()

            assertFalse(uninstalled.destinationOccupied)
            assertFalse(uninstalled.installed)
            assertFalse(uninstalled.uninstallAvailable)
            assertFalse(Files.exists(home.resolve(".agents/skills/silverwing")))
            assertEquals("another skill", Files.readString(sibling.resolve("SKILL.md")))
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `uninstalling a missing target is a no-op`() {
        val root = Files.createTempDirectory("silverwing-tag-skill-uninstall-missing")
        try {
            val home = root.resolve("home")
            val source = skillSource(root.resolve("bundle"))
            val service = PackagedTagSkillInstallationService(source = { source }, userHome = { home })

            val status = service.uninstall()

            assertFalse(status.destinationOccupied)
            assertFalse(status.uninstallAvailable)
            assertFalse(Files.exists(home.resolve(".agents/skills/silverwing")))
        } finally {
            deleteTree(root)
        }
    }

    private fun skillSource(root: Path): Path {
        Files.createDirectories(root.resolve("references"))
        Files.writeString(root.resolve("SKILL.md"), "---\nname: silverwing\n---\nTag-only content\n")
        Files.writeString(root.resolve("references/tag-builds.md"), "Tag details\n")
        return root
    }

    private fun deleteTree(path: Path) {
        if (!Files.exists(path)) return
        Files.walk(path).use { paths ->
            paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
        }
    }
}
