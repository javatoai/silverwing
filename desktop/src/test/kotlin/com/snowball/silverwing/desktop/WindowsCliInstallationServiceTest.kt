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

class WindowsCliInstallationServiceTest {
    @Test
    fun `install copies bundled cli and runtime then adds one user path entry`() {
        val root = Files.createTempDirectory("silverwing-cli-install")
        try {
            val localApplicationData = root.resolve("local-app-data")
            val bundled = bundledSource(root.resolve("bundle"), version = "0.9.10")
            val pathStore = InMemoryUserPathStore("C:\\Tools")
            var environmentNotifications = 0
            val service = WindowsCliInstallationService(
                source = { bundled },
                localApplicationData = { localApplicationData },
                userPath = pathStore,
                isWindows = { true },
                environmentChanged = { environmentNotifications += 1 },
            )

            val installed = service.install()

            val commandDirectory = localApplicationData.resolve("silverwing/bin")
            val versionDirectory = localApplicationData.resolve("silverwing/cli/0.9.10")
            assertTrue(installed.installed)
            assertEquals("0.9.10", installed.version)
            assertEquals(commandDirectory.resolve("silverwing.cmd"), installed.commandPath)
            assertTrue(Files.isRegularFile(versionDirectory.resolve("cli/bin/silverwing.cmd")))
            assertTrue(Files.isRegularFile(versionDirectory.resolve("cli/lib/silverwing.jar")))
            assertTrue(Files.isRegularFile(versionDirectory.resolve("runtime/bin/java.exe")))
            assertTrue(Files.isRegularFile(localApplicationData.resolve("silverwing/cli/.silverwing-cli-managed")))
            assertContains(Files.readString(commandDirectory.resolve("silverwing.cmd")), "SILVERWING-CLI-MANAGED: v1")
            assertContains(Files.readString(commandDirectory.resolve("silverwing.cmd")), "..\\cli\\0.9.10\\cli\\bin\\silverwing.cmd")
            assertEquals("C:\\Tools;${commandDirectory.toAbsolutePath().normalize()}", pathStore.value)
            assertEquals(1, environmentNotifications)

            service.install()
            assertEquals("C:\\Tools;${commandDirectory.toAbsolutePath().normalize()}", pathStore.value)
            assertEquals(2, environmentNotifications)
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `development mode without bundled payload does not offer installation`() {
        val root = Files.createTempDirectory("silverwing-cli-missing")
        try {
            val service = WindowsCliInstallationService(
                source = { null },
                localApplicationData = { root.resolve("local-app-data") },
                userPath = InMemoryUserPathStore(),
                isWindows = { true },
            )

            val status = service.inspect()

            assertTrue(status.supported)
            assertFalse(status.bundledPayloadAvailable)
            assertFalse(status.installed)
            assertFalse(status.uninstallAvailable)
            assertFailsWith<IllegalArgumentException> { service.install() }
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `empty bundled library directory is rejected before any installation write`() {
        val root = Files.createTempDirectory("silverwing-cli-empty-library")
        try {
            val bundled = bundledSource(root.resolve("bundle"), version = "0.9.10")
            Files.delete(bundled.cliHome.resolve("lib/silverwing.jar"))
            val localApplicationData = root.resolve("local-app-data")
            val service = WindowsCliInstallationService(
                source = { bundled },
                localApplicationData = { localApplicationData },
                userPath = InMemoryUserPathStore(),
                isWindows = { true },
            )

            assertFailsWith<IllegalArgumentException> { service.install() }
            assertFalse(service.inspect().bundledPayloadAvailable)
            assertFalse(Files.exists(localApplicationData))
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `installing a newer bundled version switches the stable command without duplicating path`() {
        val root = Files.createTempDirectory("silverwing-cli-upgrade")
        try {
            val localApplicationData = root.resolve("local-app-data")
            val pathStore = InMemoryUserPathStore()
            fun serviceFor(version: String) = WindowsCliInstallationService(
                source = { bundledSource(root.resolve("bundle-$version"), version) },
                localApplicationData = { localApplicationData },
                userPath = pathStore,
                isWindows = { true },
            )

            serviceFor("0.9.10").install()
            val upgraded = serviceFor("0.9.11").install()

            val commandDirectory = localApplicationData.resolve("silverwing/bin")
            assertTrue(upgraded.installed)
            assertEquals("0.9.11", upgraded.version)
            assertTrue(Files.isDirectory(localApplicationData.resolve("silverwing/cli/0.9.10")))
            assertTrue(Files.isDirectory(localApplicationData.resolve("silverwing/cli/0.9.11")))
            assertContains(Files.readString(commandDirectory.resolve("silverwing.cmd")), "..\\cli\\0.9.11\\cli\\bin\\silverwing.cmd")
            assertEquals(commandDirectory.toAbsolutePath().normalize().toString(), pathStore.value)
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `uninstall removes silverwing cli payload and its path entry but preserves other user files`() {
        val root = Files.createTempDirectory("silverwing-cli-uninstall")
        try {
            val localApplicationData = root.resolve("local-app-data")
            val pathStore = InMemoryUserPathStore("C:\\Tools;C:\\Other")
            var environmentNotifications = 0
            val service = WindowsCliInstallationService(
                source = { bundledSource(root.resolve("bundle"), version = "0.9.10") },
                localApplicationData = { localApplicationData },
                userPath = pathStore,
                isWindows = { true },
                environmentChanged = { environmentNotifications += 1 },
            )
            service.install()
            val commandDirectory = localApplicationData.resolve("silverwing/bin")
            Files.writeString(commandDirectory.resolve("keep.txt"), "not managed by silverwing CLI")

            val status = service.uninstall()

            assertFalse(status.installed)
            assertTrue(status.bundledPayloadAvailable)
            assertFalse(Files.exists(localApplicationData.resolve("silverwing/cli")))
            assertFalse(Files.exists(commandDirectory.resolve("silverwing.cmd")))
            assertFalse(Files.exists(commandDirectory.resolve("silverwing.version")))
            assertTrue(Files.isRegularFile(commandDirectory.resolve("keep.txt")))
            assertEquals("C:\\Tools;C:\\Other", pathStore.value)
            assertEquals(2, environmentNotifications)
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `incomplete managed payload remains uninstallable for recovery`() {
        val root = Files.createTempDirectory("silverwing-cli-partial-uninstall")
        try {
            val localApplicationData = root.resolve("local-app-data")
            val service = WindowsCliInstallationService(
                source = { bundledSource(root.resolve("bundle"), version = "0.9.10") },
                localApplicationData = { localApplicationData },
                userPath = InMemoryUserPathStore(),
                isWindows = { true },
            )
            service.install()
            Files.delete(localApplicationData.resolve("silverwing/cli/0.9.10/runtime/bin/java.exe"))

            val incomplete = service.inspect()

            assertFalse(incomplete.installed)
            assertTrue(incomplete.uninstallAvailable)
            assertTrue(service.uninstall().bundledPayloadAvailable)
            assertFalse(Files.exists(localApplicationData.resolve("silverwing/cli")))
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `custom command entry is never overwritten or removed`() {
        val root = Files.createTempDirectory("silverwing-cli-custom-command")
        try {
            val localApplicationData = root.resolve("local-app-data")
            val command = localApplicationData.resolve("silverwing/bin/silverwing.cmd")
            Files.createDirectories(command.parent)
            Files.writeString(command, "@echo custom command\r\n")
            val pathStore = InMemoryUserPathStore("C:\\Tools")
            val service = WindowsCliInstallationService(
                source = { bundledSource(root.resolve("bundle"), version = "0.9.10") },
                localApplicationData = { localApplicationData },
                userPath = pathStore,
                isWindows = { true },
            )

            assertFailsWith<IllegalArgumentException> { service.install() }
            assertEquals("@echo custom command\r\n", Files.readString(command))
            assertFalse(Files.exists(localApplicationData.resolve("silverwing/cli")))
            service.uninstall()
            assertTrue(Files.isRegularFile(command))
            assertEquals("C:\\Tools", pathStore.value)
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `quoted trailing slash path entry is reused and removed`() {
        val root = Files.createTempDirectory("silverwing-cli-normalized-path")
        try {
            val localApplicationData = root.resolve("local-app-data")
            val commandDirectory = localApplicationData.resolve("silverwing/bin").toAbsolutePath().normalize()
            val quotedExistingEntry = "\"${commandDirectory.toString().replace('/', '\\')}\\\""
            val pathStore = InMemoryUserPathStore("$quotedExistingEntry;C:\\Tools")
            val service = WindowsCliInstallationService(
                source = { bundledSource(root.resolve("bundle"), version = "0.9.10") },
                localApplicationData = { localApplicationData },
                userPath = pathStore,
                isWindows = { true },
            )

            service.install()
            assertEquals("$quotedExistingEntry;C:\\Tools", pathStore.value)
            service.uninstall()
            assertEquals("C:\\Tools", pathStore.value)
        } finally {
            deleteTree(root)
        }
    }

    @Test
    fun `non Windows host never writes installation files`() {
        val root = Files.createTempDirectory("silverwing-cli-non-windows")
        try {
            val service = WindowsCliInstallationService(
                source = { bundledSource(root.resolve("bundle"), version = "0.9.10") },
                localApplicationData = { root.resolve("local-app-data") },
                userPath = InMemoryUserPathStore(),
                isWindows = { false },
            )

            assertFalse(service.inspect().supported)
            assertFailsWith<IllegalArgumentException> { service.install() }
            assertFailsWith<IllegalArgumentException> { service.uninstall() }
            assertFalse(Files.exists(root.resolve("local-app-data")))
        } finally {
            deleteTree(root)
        }
    }

    private fun bundledSource(root: Path, version: String): PortableCliSource {
        val cliHome = root.resolve("cli")
        val runtimeHome = root.resolve("runtime")
        Files.createDirectories(cliHome.resolve("bin"))
        Files.createDirectories(cliHome.resolve("lib"))
        Files.createDirectories(runtimeHome.resolve("bin"))
        Files.writeString(cliHome.resolve("bin/silverwing.cmd"), "@echo off\r\n")
        Files.writeString(cliHome.resolve("lib/silverwing.jar"), "jar")
        Files.writeString(runtimeHome.resolve("bin/java.exe"), "runtime")
        return PortableCliSource(cliHome, runtimeHome, version)
    }

    private fun deleteTree(path: Path) {
        if (!Files.exists(path)) return
        Files.walk(path).use { paths ->
            paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
        }
    }

    private class InMemoryUserPathStore(initial: String = "") : UserPathStore {
        var value: String = initial

        override fun read(): String = value
        override fun write(value: String) {
            this.value = value
        }
    }
}
