package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class WorkspacePathAliasIntegrationTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `configured repository alias can inspect branches and create a healthy worktree`() {
        val real = Files.createDirectories(temporary.resolve("real"))
        val alias = temporary.resolve("alias")
        DirectoryAliasTestSupport.create(temporary, alias, real)
        val (remote, _) = GitTestSupport.createRemoteWithSeed(real)
        val source = GitTestSupport.clone(remote, real.resolve("source"))
        val repository = GitRepositoryInspector().inspect(source).copy(
            rootPath = alias.resolve("source").toString(),
            gitCommonDirectory = alias.resolve("source/.git").toString(),
        )
        val locks = RepositoryOperationLock(ApplicationPaths(temporary.resolve("app")))
        val service = GroupServiceConfig.standard("alias-service", repository.id, "alias", baseRef = "origin/master")
        val branch = "feature/alias"

        assertTrue(WorkspaceBranchReuseInspector(repositoryLock = locks).inspect(repository, service, branch).isEmpty())
        val workspace = StandardWorktreeProvisioner(repositoryLock = locks).provision(
            WorkspaceProvisionRequest(temporary.resolve("task"), repository, service, branch),
        ).single()

        assertEquals(branch, GitClient().currentBranch(Path.of(workspace.worktreePath)))
        val health = GitWorkspaceGitStatusReader().read(workspace)
        assertEquals(WorkspaceGitHealthState.READY, health.state)
    }
}
