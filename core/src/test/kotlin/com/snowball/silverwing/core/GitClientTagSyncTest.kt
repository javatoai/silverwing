package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import org.junit.jupiter.api.io.TempDir

class GitClientTagSyncTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `tag fetch mirrors remote tag refs and prunes local-only tags`() {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("source"))
        GitTestSupport.run(seed, "branch", "release/test")
        GitTestSupport.run(seed, "push", "origin", "release/test")
        GitTestSupport.run(seed, "tag", "-a", "3.21.43.beta-3", "-m", "old beta-3")
        GitTestSupport.run(seed, "tag", "-a", "3.21.43.beta-7", "-m", "beta-7")
        GitTestSupport.run(seed, "push", "origin", "--tags")

        val repository = GitTestSupport.clone(remote, temporary.resolve("clone"))
        val git = GitClient()
        GitTestSupport.run(repository, "tag", "-a", "3.21.43.beta-99", "-m", "local-only")
        val targetBefore = git.resolve(repository, "refs/remotes/origin/release/test")

        Files.writeString(seed.resolve("remote-tag-target.txt"), "remote tag target\n")
        GitTestSupport.run(seed, "add", "remote-tag-target.txt")
        GitTestSupport.run(seed, "commit", "-m", "move remote beta-3")
        GitTestSupport.run(seed, "tag", "-f", "-a", "3.21.43.beta-3", "-m", "new beta-3")
        GitTestSupport.run(seed, "push", "--force", "origin", "refs/tags/3.21.43.beta-3")
        val remoteBeta3 = GitTestSupport.run(seed, "rev-parse", "3.21.43.beta-3^{}").trim()

        git.fetchTags(repository)

        assertEquals(remoteBeta3, git.resolve(repository, "refs/tags/3.21.43.beta-3"))
        assertEquals(targetBefore, git.resolve(repository, "refs/remotes/origin/release/test"))
        assertFalse(git.refExists(repository, "refs/tags/3.21.43.beta-99"))
        assertEquals(
            GitTestSupport.run(repository, "rev-parse", "3.21.43.beta-7^{}").trim(),
            GitTestSupport.run(seed, "rev-parse", "3.21.43.beta-7^{}").trim(),
        )
    }

    @Test
    fun `branch fetch remains non forcing and tag fetch is explicitly authoritative`() {
        val commands = mutableListOf<List<String>>()
        val runner = object : CommandRunner {
            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: java.time.Duration,
                environment: Map<String, String>,
            ): CommandResult {
                commands += command
                return CommandResult(0, "", "")
            }
        }
        val git = GitClient(runner)

        git.fetch(Path.of("repository"))
        assertEquals(
            listOf("fetch", "--prune", "--no-tags", "origin"),
            commands.single().takeLast(4),
        )

        git.fetchTags(Path.of("repository"))
        val tagCommand = commands.last().takeLast(6)
        assertEquals(listOf("fetch", "--tags", "--prune", "--prune-tags", "--force", "origin"), tagCommand)
        assertTrue("--force" in tagCommand)
        assertFalse("--no-force" in tagCommand)
    }
}
