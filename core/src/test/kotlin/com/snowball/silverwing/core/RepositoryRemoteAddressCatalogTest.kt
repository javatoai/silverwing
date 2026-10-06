package com.snowball.silverwing.core

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.nio.file.Files
import java.time.Duration
import java.util.concurrent.CancellationException
import kotlin.test.*

class RepositoryRemoteAddressCatalogTest {
    @TempDir lateinit var repository: Path

    private class Commands(private val block: (List<String>) -> CommandResult) : CommandRunner {
        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration,
            environment: Map<String, String>) = block(command)
    }

    @Test fun `timeouts and ordinary failures identify each read stage without retaining diagnostics`() {
        val sensitive = "https://user:credential@example.invalid/private.git?token=credential remote-secret git-secret"
        for (stage in RepositoryRemoteAddressReadStage.entries) for (timedOut in listOf(false, true)) {
            val commands = Commands { command ->
                val actualStage = when {
                    command.last() == "remote" -> RepositoryRemoteAddressReadStage.REMOTE_LIST
                    "--push" in command -> RepositoryRemoteAddressReadStage.PUSH_ADDRESSES
                    else -> RepositoryRemoteAddressReadStage.FETCH_ADDRESSES
                }
                if (actualStage == stage) CommandResult(if (timedOut) 124 else 1, sensitive, sensitive)
                else CommandResult(0, if (actualStage == RepositoryRemoteAddressReadStage.REMOTE_LIST) "remote-secret\n" else sensitive.substringBefore(' '), "")
            }
            val error = assertFailsWith<RepositoryRemoteAddressReadException> {
                GitRepositoryRemoteAddressCatalog(GitClient(commands, GitExecutable { "git-secret" })).list(repository)
            }
            val failure = RepositoryRemoteAddressFailure(stage, if (timedOut) RepositoryRemoteAddressFailureKind.TIMEOUT else RepositoryRemoteAddressFailureKind.OTHER)
            assertEquals(failure, error.failure)
            assertEquals(failure.message, error.message)
            assertEquals(timedOut, "超时" in error.message!!)
            assertNull(error.cause)
            assertTrue(error.suppressed.isEmpty())
            for (secret in listOf("credential", "remote-secret", "git-secret", "example.invalid", repository.toString())) {
                assertFalse(secret in error.toString(), "Unsafe diagnostic retained for $stage")
                assertFalse(secret in error.failure.toString(), "Unsafe failure metadata retained for $stage")
            }
        }
    }

    @Test fun `startup errors are sanitized while interruption and cancellation propagate unchanged`() {
        val unsafe = IllegalStateException("credential and command arguments", IllegalArgumentException("private address"))
        val catalog = GitRepositoryRemoteAddressCatalog(GitClient(Commands { throw unsafe }, GitExecutable { "git-test" }))
        val error = assertFailsWith<RepositoryRemoteAddressReadException> { catalog.list(repository) }
        assertEquals(RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.REMOTE_LIST, RepositoryRemoteAddressFailureKind.OTHER), error.failure)
        assertNull(error.cause)
        assertFalse("credential" in error.toString())
        for (cancelled in listOf(InterruptedException("interrupted"), CancellationException("cancelled"))) {
            val interruptedCatalog = GitRepositoryRemoteAddressCatalog(GitClient(Commands { throw cancelled }, GitExecutable { "git-test" }))
            assertSame(cancelled, assertFails { interruptedCatalog.list(repository) })
        }
    }

    @Test fun `encoded repository identifiers keep their raw separators and strip encoded git suffixes`() {
        val cases = mapOf(
            "https://user:secret@example.invalid/org%2Fnested%2Frepo.git?token=secret" to "https://example.invalid/org%2Fnested%2Frepo",
            "ssh://git@example.invalid:2222/org%2Frepo%2E%67%69%74" to "https://example.invalid/org%2Frepo",
            "https://example.invalid:8443/org/repo%252Fname.git" to "https://example.invalid:8443/org/repo%252Fname",
            "https://example.invalid/org/name%3Fpart%23part.git" to "https://example.invalid/org/name%3Fpart%23part",
        )
        cases.forEach { (input, expected) -> assertEquals(expected, gitRemoteBrowserUrl(input), input) }
    }

    @Test fun `query and fragment at signs cannot be misinterpreted as authority credentials`() {
        for (input in listOf("https://example.invalid?token=user@secret", "https://user:secret@example.invalid#token=user@secret")) {
            assertEquals("https://example.invalid", sanitizeGitRemoteUrl(input))
            assertNull(gitRemoteBrowserUrl(input))
        }
        GitTestSupport.run(repository, "init")
        GitTestSupport.run(repository, "remote", "add", "origin", "https://example.invalid?token=user@secret")
        assertEquals("https://example.invalid", GitRepositoryRemoteAddressCatalog().list(repository).single().gitUrl)
    }

    @Test fun `reads every URL locally with fetch push deduplication and remote order`() {
        GitTestSupport.run(repository, "init")
        val longUrl = "https://example.invalid/" + "nested/".repeat(60) + "repo.git"
        GitTestSupport.run(repository, "remote", "add", "upstream", longUrl)
        GitTestSupport.run(repository, "remote", "add", "github", "git@github.com:org/repo.git")
        GitTestSupport.run(repository, "remote", "add", "origin", "https://example.invalid/org/a.git")
        GitTestSupport.run(repository, "config", "--add", "remote.origin.url", "https://example.invalid/org/b.git")
        GitTestSupport.run(repository, "config", "--add", "remote.origin.url", "https://example.invalid/org/a.git")
        GitTestSupport.run(repository, "config", "--add", "remote.origin.pushurl", "https://example.invalid/org/b.git")
        GitTestSupport.run(repository, "config", "--add", "remote.origin.pushurl", "ssh://git@example.invalid:2222/org/write.git")
        val values = GitRepositoryRemoteAddressCatalog().list(repository)
        assertEquals(listOf("origin", "origin", "origin", "github", "upstream"), values.map { it.remote })
        assertEquals(listOf(true to false, true to true, false to true), values.take(3).map { it.fetch to it.push })
        assertEquals(listOf("https://example.invalid/org/a.git", "https://example.invalid/org/b.git", "ssh://example.invalid:2222/org/write.git"), values.take(3).map { it.gitUrl })
        assertEquals(longUrl, values.last().gitUrl)
        assertTrue(values.last().fetch && values.last().push)
    }

    @Test fun `empty repository and single remote are valid`() {
        GitTestSupport.run(repository, "init")
        val catalog = GitRepositoryRemoteAddressCatalog()
        assertTrue(catalog.list(repository).isEmpty())
        GitTestSupport.run(repository, "remote", "add", "origin", "https://user:secret@example.invalid/team/repo.git?token=secret")
        assertEquals(listOf(RepositoryRemoteAddress("origin", "https://example.invalid/team/repo.git", true, true)), catalog.list(repository))
    }

    @Test fun `read failure is safe and does not expose command output`() {
        val error = assertFailsWith<RepositoryRemoteAddressReadException> { GitRepositoryRemoteAddressCatalog().list(repository.resolve("missing")) }
        assertEquals("远程列表读取失败，请检查本地路径和 Git 配置", error.message)
        assertNull(error.cause)
    }

    @Test fun `local repository hash names survive remote reading and copying`() {
        GitTestSupport.run(repository, "init")
        val source = Files.createDirectory(repository.resolve("source#1.git"))
        GitTestSupport.run(source, "init", "--bare")
        GitTestSupport.run(repository, "remote", "add", "origin", source.toString())
        GitTestSupport.run(repository, "ls-remote", "origin")
        val remote = GitRepositoryRemoteAddressCatalog().list(repository).single()
        assertEquals(source.toString(), remote.gitUrl)
        assertNull(gitRemoteBrowserUrl(remote.gitUrl))
    }

    @Test fun `path punctuation is retained without retaining scp usernames`() {
        listOf("C:\\repos\\repo#one.git", "./repo#one?two.git", "/srv/repo#one?two.git", "file:///srv/repo#one?two.git").forEach {
            assertEquals(it, sanitizeGitRemoteUrl(it), it)
            assertNull(gitRemoteBrowserUrl(it), it)
        }
        assertEquals("github.com:team/email@repo#one?two.git", sanitizeGitRemoteUrl("git@github.com:team/email@repo#one?two.git"))
        assertEquals("https://github.com/team/email@repo%23one%3Ftwo", gitRemoteBrowserUrl("git@github.com:team/email@repo#one?two.git"))
    }

    @Test fun `browser destinations strip credentials ssh ports and git suffix`() {
        val cases = mapOf(
            "https://user:password@example.invalid:8443/a/b/repo.git?access_token=secret#fragment" to "https://example.invalid:8443/a/b/repo",
            "http://example.invalid/a/repo.git/" to "http://example.invalid/a/repo",
            "git@github.com:org/subgroup/repo.git" to "https://github.com/org/subgroup/repo",
            "ssh://user:password@gitlab.example.invalid:2222/org/nested/repo.git" to "https://gitlab.example.invalid/org/nested/repo",
            "ssh://git@[2001:db8::1]:22/org/repo.git" to "https://[2001:db8::1]/org/repo",
        )
        cases.forEach { (input, expected) -> assertEquals(expected, gitRemoteBrowserUrl(input), input) }
        listOf("C:\\repos\\project", "/srv/repo.git", "../repo.git", "file:///srv/repo.git", "git://example.invalid/repo.git", "helper::user@host/path", "https://bad host/a", "https://host/").forEach { assertNull(gitRemoteBrowserUrl(it), it) }
        assertEquals("https://example.invalid/a.git", sanitizeGitRemoteUrl("https://user:secret@example.invalid/a.git?token=secret"))
        assertEquals("https://example.invalid/a.git", sanitizeGitRemoteUrl("https://user:secret?part#part@example.invalid/a.git?token=secret"))
        assertEquals("v://example.invalid/a.git", sanitizeGitRemoteUrl("v://user:secret@example.invalid/a.git?token=secret"))
        assertEquals("github.com:org/repo.git", sanitizeGitRemoteUrl("git@github.com:org/repo.git"))
    }
}
