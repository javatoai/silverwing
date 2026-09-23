package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import org.junit.jupiter.api.io.TempDir

class WorkspaceGitHistoryTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun parserPreservesMultilineMessagesAndParsesOffsetTimestamps() {
        val output = listOf(
            "abcdef123\u00002026-09-14T15:58:05+08:00\u0000Committer\u0000committer@example.test\u0000Author\u0000author@example.test\u0000subject\n\nbody line\n\u0000",
            "1234567\u00002026-09-13T07:00:00Z\u0000Another Author\u0000same@example.test\u0000Another Author\u0000same@example.test\u0000second message\n\u0000",
        ).joinToString("\n")

        val commits = WorkspaceGitCommitLogParser.parse(output)

        assertEquals(2, commits.size)
        assertEquals("abcdef1", commits[0].shortHash)
        assertEquals("2026-09-14T07:58:05Z", commits[0].committedAt.toString())
        assertEquals("abcdef123", commits[0].fullHash)
        assertEquals("Committer", commits[0].committerName)
        assertEquals("committer@example.test", commits[0].committerEmail)
        assertEquals("Author", commits[0].authorName)
        assertEquals("author@example.test", commits[0].authorEmail)
        assertEquals("subject\n\nbody line", commits[0].message)
        assertEquals("1234567", commits[1].shortHash)
        assertEquals("Another Author", commits[1].authorName)
        assertEquals("second message", commits[1].message)
    }

    @Test
    fun readerUsesLocalHeadLogWithoutMergeExclusionOrRemoteOperations() {
        val runner = RecordingCommandRunner(
            CommandResult(
                0,
                "abcdef123\u00002026-09-14T15:58:05+08:00\u0000Committer\u0000c@example.test\u0000Author\u0000a@example.test\u0000commit message\n\u0000\n",
                "",
            ),
        )
        val reader = GitWorkspaceGitHistoryReader(
            GitClient(runner, GitExecutable { "git" }),
        )

        val commits = reader.read(temporary)

        assertEquals(1, commits.size)
        val command = runner.commands.single()
        assertTrue(command.contains("log"))
        assertTrue(command.contains("HEAD"))
        assertTrue(command.any { "--format=%H%x00%cI%x00%cn%x00%ce%x00%an%x00%ae%x00%B%x00" == it })
        assertTrue(command.none { it == "--no-merges" || it == "fetch" })
    }

    @Test
    fun readerTreatsAnEmptyRepositoryAsEmptyHistory() {
        val runner = RecordingCommandRunner(
            CommandResult(128, "", "fatal: your current branch 'master' does not have any commits yet"),
        )

        val history = GitWorkspaceGitHistoryReader(GitClient(runner, GitExecutable { "git" })).read(temporary)

        assertTrue(history.isEmpty())
    }

    @Test
    fun readerReportsUnexpectedGitErrors() {
        val runner = RecordingCommandRunner(CommandResult(128, "", "fatal: not a git repository"))

        assertFailsWith<GitException> {
            GitWorkspaceGitHistoryReader(GitClient(runner, GitExecutable { "git" })).read(temporary)
        }
    }

    @Test
    fun readerDoesNotTreatAnUnrelatedUnknownRevisionAsAnEmptyRepository() {
        val runner = RecordingCommandRunner(CommandResult(128, "", "fatal: unknown revision 'HEAD'"))

        assertFailsWith<GitException> {
            GitWorkspaceGitHistoryReader(GitClient(runner, GitExecutable { "git" })).read(temporary)
        }
    }

    @Test
    fun readerReturnsNewestReachableCommitsIncludingMergeCommits() {
        val repository = temporary.resolve("history-repository")
        Files.createDirectories(repository)
        GitTestSupport.run(temporary, "init", repository.toString())
        GitTestSupport.configureIdentity(repository)

        Files.writeString(repository.resolve("README.md"), "initial\n")
        GitTestSupport.run(repository, "add", "README.md")
        GitTestSupport.run(repository, "commit", "-m", "initial")
        GitTestSupport.run(repository, "branch", "feature/history")
        GitTestSupport.run(repository, "switch", "feature/history")
        Files.writeString(repository.resolve("feature.txt"), "feature\n")
        GitTestSupport.run(repository, "add", "feature.txt")
        GitTestSupport.run(repository, "commit", "-m", "feature change")
        GitTestSupport.run(repository, "switch", "master")
        Files.writeString(repository.resolve("base.txt"), "base\n")
        GitTestSupport.run(repository, "add", "base.txt")
        GitTestSupport.run(repository, "commit", "-m", "base change")
        GitTestSupport.run(repository, "merge", "--no-ff", "feature/history", "-m", "merge feature\n\nmerge body")

        val commits = GitWorkspaceGitHistoryReader().read(repository)

        assertEquals(4, commits.size)
        assertEquals("merge feature\n\nmerge body", commits.first().message)
        assertEquals("silverwing Tests", commits.first().authorName)
        assertEquals("silverwing Tests", commits.first().committerName)
        assertEquals(40, commits.first().fullHash.length)
        assertTrue(commits.any { it.message == "feature change" })
        assertTrue(commits.any { it.message == "base change" })
        assertTrue(commits.all { it.shortHash.length == 7 })
        assertTrue(commits.zipWithNext().all { (newer, older) -> newer.committedAt >= older.committedAt })
    }

    @Test
    fun readerComparesAgainstLocalRemoteTrackingRefWithoutFetching() {
        val repository = temporary.resolve("relative-history")
        Files.createDirectories(repository)
        GitTestSupport.run(temporary, "init", repository.toString())
        GitTestSupport.configureIdentity(repository)
        Files.writeString(repository.resolve("base.txt"), "base")
        GitTestSupport.run(repository, "add", "base.txt")
        GitTestSupport.run(repository, "commit", "-m", "baseline")
        val baseline = GitTestSupport.run(repository, "rev-parse", "HEAD")
        GitTestSupport.run(repository, "update-ref", "refs/remotes/origin/master", baseline)
        Files.writeString(repository.resolve("feature.txt"), "feature")
        GitTestSupport.run(repository, "add", "feature.txt")
        GitTestSupport.run(repository, "commit", "-m", "feature only")

        val reader = GitWorkspaceGitHistoryReader()
        assertEquals(listOf("feature only"), reader.read(repository, "origin/master").map { it.message })
        assertEquals(2, reader.read(repository).size)
        assertFailsWith<IllegalArgumentException> { reader.read(repository, "origin/missing") }
    }

    private class RecordingCommandRunner(
        private val result: CommandResult,
    ) : CommandRunner {
        val commands = mutableListOf<List<String>>()

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            commands += command
            return result
        }
    }
}
