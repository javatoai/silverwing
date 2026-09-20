package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path

class RepositoryInspectorIntegrationTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `selected child directory is normalized to its main git repository`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("valid"))
        val repository = GitTestSupport.clone(remote, temporary.resolve("valid").resolve("service"))
        val child = repository.resolve("module-a")
        Files.createDirectories(child)

        val result = GitRepositoryInspector().inspect(child)

        assertEquals(repository.toRealPath().toString(), Path.of(result.rootPath).toRealPath().toString())
        assertEquals(remote.toRealPath().toString(), Path.of(result.originUrl!!).toRealPath().toString())
    }

    @Test
    fun `non git directory is rejected`() {
        val directory = temporary.resolve("plain")
        Files.createDirectories(directory)

        assertThrows(IllegalArgumentException::class.java) {
            GitRepositoryInspector().inspect(directory)
        }
    }

    @Test
    fun `linked worktree is rejected as a catalog source`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("linked"))
        val repository = GitTestSupport.clone(remote, temporary.resolve("linked").resolve("service"))
        val worktree = temporary.resolve("linked").resolve("feature")
        GitTestSupport.run(repository, "worktree", "add", "-b", "feature/test", worktree.toString())

        assertThrows(IllegalArgumentException::class.java) {
            GitRepositoryInspector().inspect(worktree)
        }
    }

    @Test
    fun `ssh username is accepted while http embedded credentials are rejected`() {
        val repository = temporary.resolve("credentials")
        Files.createDirectories(repository)
        GitTestSupport.run(temporary, "init", repository.toString())
        GitTestSupport.run(repository, "remote", "add", "origin", "ssh://git@github.com/example/repo.git")
        assertEquals("ssh://git@github.com/example/repo.git", GitRepositoryInspector().inspect(repository).originUrl)

        GitTestSupport.run(repository, "remote", "set-url", "origin", "https://user:secret@example.test/repo.git")
        assertThrows(IllegalArgumentException::class.java) { GitRepositoryInspector().inspect(repository) }
    }
}
