package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path

internal object GitTestSupport {
    private val isolatedGlobalConfig: Path by lazy {
        Files.createTempFile("silverwing-git-test-global-", ".config").also { path ->
            Files.writeString(path, "# silverwing Git fixtures intentionally ignore user and system configuration\n")
            path.toFile().deleteOnExit()
        }
    }

    fun run(directory: Path, vararg arguments: String): String {
        val builder = ProcessBuilder(listOf("git", "-C", directory.toString()) + arguments)
            .redirectErrorStream(true)
        builder.environment()["GIT_CONFIG_GLOBAL"] = isolatedGlobalConfig.toString()
        builder.environment()["GIT_CONFIG_NOSYSTEM"] = "1"
        builder.environment()["GIT_TERMINAL_PROMPT"] = "0"
        val process = builder.start()
        val output = process.inputStream.bufferedReader().use { it.readText() }
        check(process.waitFor() == 0) {
            "git ${arguments.joinToString(" ")} failed in $directory:\n$output"
        }
        return output.trim()
    }

    fun createRemoteWithSeed(root: Path): Pair<Path, Path> {
        val remote = root.resolve("remote.git")
        val seed = root.resolve("seed")
        Files.createDirectories(remote)
        Files.createDirectories(seed)
        run(root, "init", "--bare", remote.toString())
        run(root, "init", seed.toString())
        run(seed, "symbolic-ref", "HEAD", "refs/heads/master")
        configureRepository(remote)
        configureIdentity(seed)
        Files.writeString(seed.resolve("README.md"), "seed\n")
        run(seed, "add", "README.md")
        run(seed, "commit", "-m", "seed")
        run(seed, "remote", "add", "origin", remote.toString())
        run(seed, "push", "-u", "origin", "master")
        run(remote, "symbolic-ref", "HEAD", "refs/heads/master")
        return remote to seed
    }

    fun clone(remote: Path, target: Path): Path {
        Files.createDirectories(target.parent)
        run(target.parent, "clone", remote.toString(), target.toString())
        configureIdentity(target)
        return target
    }

    fun configureIdentity(repository: Path) {
        configureRepository(repository)
        run(repository, "config", "user.name", "silverwing Tests")
        run(repository, "config", "user.email", "silverwing-tests@example.invalid")
    }

    private fun configureRepository(repository: Path) {
        run(repository, "config", "core.autocrlf", "false")
        run(repository, "config", "commit.gpgsign", "false")
        run(repository, "config", "tag.gpgSign", "false")
    }
}
