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

class MacCliInstallationServiceTest {
    @Test
    fun `install makes silverwing available from a managed zprofile without touching user content`() {
        val root = Files.createTempDirectory("silverwing-mac-cli-install")
        try {
            val home = root.resolve("home")
            val profile = home.resolve(".zprofile")
            Files.createDirectories(home)
            Files.writeString(profile, "export EDITOR=vim\n")
            val service = service(home, profile, bundledSource(root.resolve("bundle"), "0.9.10"))

            val installed = service.install()
            val commandDirectory = home.resolve("Library/Application Support/silverwing/bin")
            val versionDirectory = home.resolve("Library/Application Support/silverwing/cli/0.9.10")

            assertTrue(installed.installed)
            assertEquals(commandDirectory.resolve("silverwing"), installed.commandPath)
            assertTrue(Files.isExecutable(commandDirectory.resolve("silverwing")))
            assertTrue(Files.isExecutable(versionDirectory.resolve("cli/bin/silverwing")))
            assertTrue(Files.isExecutable(versionDirectory.resolve("runtime/bin/java")))
            assertContains(Files.readString(commandDirectory.resolve("silverwing")), "SILVERWING-CLI-MANAGED: v1")
            assertContains(Files.readString(commandDirectory.resolve("silverwing")), "../cli/0.9.10/cli/bin/silverwing")

            val profileContent = Files.readString(profile)
            assertContains(profileContent, "export EDITOR=vim")
            assertContains(profileContent, "# >>> silverwing CLI >>>")
            assertContains(profileContent, commandDirectory.toAbsolutePath().normalize().toString())

            service.install()
            assertEquals(1, Regex("# >>> silverwing CLI >>>").findAll(Files.readString(profile)).count())
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `uninstall removes only the managed command payload and profile block`() {
        val root = Files.createTempDirectory("silverwing-mac-cli-uninstall")
        try {
            val home = root.resolve("home")
            val profile = home.resolve(".zprofile")
            Files.createDirectories(home)
            Files.writeString(profile, "export EDITOR=vim\n")
            val service = service(home, profile, bundledSource(root.resolve("bundle"), "0.9.10"))
            service.install()

            val commandDirectory = home.resolve("Library/Application Support/silverwing/bin")
            Files.writeString(commandDirectory.resolve("keep.txt"), "not managed by silverwing CLI")
            val status = service.uninstall()

            assertFalse(status.installed)
            assertFalse(Files.exists(home.resolve("Library/Application Support/silverwing/cli")))
            assertFalse(Files.exists(commandDirectory.resolve("silverwing")))
            assertFalse(Files.exists(commandDirectory.resolve("silverwing.version")))
            assertTrue(Files.isRegularFile(commandDirectory.resolve("keep.txt")))
            assertEquals("export EDITOR=vim\n", Files.readString(profile))
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `custom silverwing command is never overwritten`() {
        val root = Files.createTempDirectory("silverwing-mac-cli-custom-command")
        try {
            val home = root.resolve("home")
            val profile = home.resolve(".zprofile")
            val command = home.resolve("Library/Application Support/silverwing/bin/silverwing")
            Files.createDirectories(command.parent)
            Files.writeString(command, "#!/usr/bin/env sh\necho custom\n")
            val service = service(home, profile, bundledSource(root.resolve("bundle"), "0.9.10"))

            assertFailsWith<IllegalArgumentException> { service.install() }
            assertContains(Files.readString(command), "echo custom")
            assertFalse(Files.exists(home.resolve("Library/Application Support/silverwing/cli")))
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `non mac host does not write installation files`() {
        val root = Files.createTempDirectory("silverwing-mac-cli-unsupported")
        try {
            val home = root.resolve("home")
            val service = MacCliInstallationService(
                source = { bundledSource(root.resolve("bundle"), "0.9.10") },
                userHome = { home },
                profilePath = { home.resolve(".zprofile") },
                isMacOs = { false },
            )

            assertFalse(service.inspect().supported)
            assertFailsWith<IllegalArgumentException> { service.install() }
            assertFailsWith<IllegalArgumentException> { service.uninstall() }
            assertFalse(Files.exists(home))
        } finally {
            deleteTree(root)
        }
    }

    private fun service(home: Path, profile: Path, bundled: PortableCliSource): MacCliInstallationService =
        MacCliInstallationService(
            source = { bundled },
            userHome = { home },
            shellName = { "/bin/zsh" },
            profilePath = { profile },
            isMacOs = { true },
        )

    private fun bundledSource(root: Path, version: String): PortableCliSource {
        val cliHome = root.resolve("cli")
        val runtimeHome = root.resolve("runtime")
        Files.createDirectories(cliHome.resolve("bin"))
        Files.createDirectories(cliHome.resolve("lib"))
        Files.createDirectories(runtimeHome.resolve("bin"))
        Files.writeString(cliHome.resolve("bin/silverwing"), "#!/usr/bin/env sh\nexit 0\n")
        Files.writeString(cliHome.resolve("lib/silverwing.jar"), "jar")
        Files.writeString(runtimeHome.resolve("bin/java"), "runtime")
        return PortableCliSource(cliHome, runtimeHome, version)
    }

    private fun deleteTree(path: Path) {
        if (!Files.exists(path)) return
        Files.walk(path).use { paths ->
            paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
        }
    }
}
