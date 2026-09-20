package com.snowball.silverwing.core

import java.nio.charset.StandardCharsets
import java.nio.file.FileSystemException
import java.nio.file.Files
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.util.Base64
import java.util.concurrent.TimeUnit
import org.junit.jupiter.api.Assumptions.assumeTrue

internal object DirectoryAliasTestSupport {
    fun create(temporary: Path, alias: Path, target: Path) {
        val root = temporary.toRealPath()
        val realTarget = target.toRealPath()
        val normalizedAlias = alias.toAbsolutePath().normalize()
        val aliasParent = normalizedAlias.parent.toRealPath()
        val realAlias = aliasParent.resolve(normalizedAlias.fileName)
        require(realTarget != root && realTarget.startsWith(root) && Files.isDirectory(realTarget)) {
            "Alias target must be a directory inside the test temporary directory: $target"
        }
        require(aliasParent.startsWith(root) && !aliasParent.startsWith(realTarget)) {
            "Alias must remain inside the test temporary directory without pointing to an ancestor: $alias"
        }
        require(!Files.exists(realAlias, NOFOLLOW_LINKS)) { "Alias path already exists: $alias" }

        val windows = System.getProperty("os.name").startsWith("Windows", ignoreCase = true)
        try {
            Files.createSymbolicLink(realAlias, realTarget)
        } catch (error: UnsupportedOperationException) {
            if (!windows) assumeTrue(false, "Directory symbolic links are unsupported: $error")
            createWindowsJunction(realAlias, realTarget, error)
        } catch (error: FileSystemException) {
            if (!windows) throw error
            // Windows normally requires a privilege for symlinks; junctions do not require it.
            // A failed fallback remains a test failure unless Windows explicitly reports no support.
            createWindowsJunction(realAlias, realTarget, error)
        }
        check(Files.isSameFile(realAlias, realTarget)) { "Directory alias does not identify its target: $alias -> $target" }
    }

    private fun createWindowsJunction(alias: Path, target: Path, symlinkError: Exception) {
        fun quote(path: Path) = "'${path.toString().replace("'", "''")}'"
        val script = """
            ${'$'}ErrorActionPreference = 'Stop'
            try {
                New-Item -ItemType Junction -Path ${quote(alias)} -Target ${quote(target)} | Out-Null
            } catch {
                ${'$'}failure = ${'$'}_.Exception
                while (${'$'}null -ne ${'$'}failure) {
                    if (${'$'}failure -is [System.PlatformNotSupportedException] -or
                        ${'$'}failure.HResult -in @(-2147024895, -2147024846) -or
                        (${'$'}failure -is [System.ComponentModel.Win32Exception] -and ${'$'}failure.NativeErrorCode -in @(1, 50))) {
                        [Console]::Error.WriteLine(${'$'}_.ToString())
                        exit 77
                    }
                    ${'$'}failure = ${'$'}failure.InnerException
                }
                throw
            }
        """.trimIndent()
        val encoded = Base64.getEncoder().encodeToString(script.toByteArray(StandardCharsets.UTF_16LE))
        val process = ProcessBuilder("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded)
            .redirectErrorStream(true)
            .start()
        if (!process.waitFor(20, TimeUnit.SECONDS)) {
            process.destroyForcibly()
            throw IllegalStateException("Timed out creating test directory junction: $alias -> $target", symlinkError)
        }
        val output = process.inputStream.bufferedReader().use { it.readText() }
        // ERROR_INVALID_FUNCTION / ERROR_NOT_SUPPORTED indicate a filesystem without junction support.
        assumeTrue(process.exitValue() != 77, "Directory aliases are unsupported on this filesystem: $output")
        check(process.exitValue() == 0) {
            "Failed to create test junction $alias -> $target (symlink failed: $symlinkError):\n$output"
        }
    }
}
