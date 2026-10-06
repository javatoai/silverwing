package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path

class WorkspaceProvisionerIntegrationTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `worktree reuse inspection includes the configured base remote`() {
        val (origin, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("base-remote-origin"))
        val (upstream, upstreamSeed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("base-remote-upstream"))
        val targetBranch = "feature/existing-upstream"
        GitTestSupport.run(upstreamSeed, "switch", "-c", targetBranch)
        GitTestSupport.run(upstreamSeed, "push", "-u", "origin", targetBranch)
        val repositoryPath = GitTestSupport.clone(origin, temporary.resolve("base-remote-source"))
        GitTestSupport.run(repositoryPath, "remote", "add", "upstream", upstream.toString())
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig(
            id = "base-remote-service",
            repositoryId = repository.id,
            displayName = "base-remote-service",
            modules = listOf(
                ServiceModuleConfig(
                    id = "default",
                    masterBranch = "upstream/master",
                    tagEnabled = false,
                    tagTargetRef = null,
                ),
            ),
        )

        val conflict = WorkspaceBranchReuseInspector().inspect(repository, service, targetBranch).single()

        assertEquals(listOf("upstream/$targetBranch"), conflict.remoteRefs)
    }

    @Test
    fun `disabled tag does not require or fetch a tag target remote`() {
        val (origin, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("disabled-tag-origin"))
        val repositoryPath = GitTestSupport.clone(origin, temporary.resolve("disabled-tag-source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig(
            id = "disabled-tag-service",
            repositoryId = repository.id,
            displayName = "disabled-tag-service",
            modules = listOf(
                ServiceModuleConfig(
                    id = "default",
                    masterBranch = "origin/master",
                    tagEnabled = false,
                    tagTargetRef = null,
                ),
            ),
        )

        assertTrue(WorkspaceBranchReuseInspector().inspect(repository, service, "feature/new").isEmpty())
    }

    @Test
    fun `selected GitHub source can list and provision while GitLab tag remote is unavailable`() {
        val root = temporary.resolve("isolated-remote-new")
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root)
        val source = GitTestSupport.clone(remote, root.resolve("source"))
        GitTestSupport.run(source, "remote", "rename", "origin", "github")
        val unavailableGitlab = root.resolve("missing-gitlab.git").toString()
        GitTestSupport.run(source, "remote", "add", "origin", unavailableGitlab)
        val repository = GitRepositoryInspector().inspect(source)
        val service = GroupServiceConfig.standard("service", repository.id, "Service", masterBranch = "github/master")
        val catalog = GitRemoteBranchCatalog()

        assertThrows(GitException::class.java) { catalog.list(source, "origin") }
        assertEquals(listOf("github/master"), catalog.list(source, "github"))
        assertTrue(WorkspaceBranchReuseInspector().inspect(repository, service, "feature/new").isEmpty())
        val workspace = StandardWorktreeProvisioner().provision(
            WorkspaceProvisionRequest(root.resolve("task"), repository, service, "feature/new"),
        ).single()

        assertEquals("github/master", workspace.baseRef)
        assertEquals("github", workspace.pushRemote)
        assertEquals(GitClient().resolve(seed, "master"), GitClient().resolve(Path.of(workspace.worktreePath), "HEAD"))
        assertEquals("origin/release/test", workspace.tagTargetRef)
        assertEquals(unavailableGitlab, GitClient().remoteUrl(source, "origin"))
    }

    @Test
    fun `GitHub branch reuse still requires confirmation when GitLab tag remote is unavailable`() {
        val root = temporary.resolve("isolated-remote-reuse")
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root)
        val branch = "feature/existing"
        GitTestSupport.run(seed, "switch", "-c", branch)
        Files.writeString(seed.resolve("feature.txt"), "feature\n")
        GitTestSupport.run(seed, "add", "feature.txt")
        GitTestSupport.run(seed, "commit", "-m", "feature")
        GitTestSupport.run(seed, "push", "origin", branch)
        val source = GitTestSupport.clone(remote, root.resolve("source"))
        GitTestSupport.run(source, "remote", "rename", "origin", "github")
        GitTestSupport.run(source, "remote", "add", "origin", root.resolve("missing-gitlab.git").toString())
        val repository = GitRepositoryInspector().inspect(source)
        val service = GroupServiceConfig.standard("service", repository.id, "Service", masterBranch = "github/master")
        val conflict = WorkspaceBranchReuseInspector().inspect(repository, service, branch).single()

        assertEquals(listOf("github/$branch"), conflict.remoteRefs)
        assertThrows(IllegalArgumentException::class.java) {
            StandardWorktreeProvisioner().provision(WorkspaceProvisionRequest(root.resolve("unconfirmed-task"), repository, service, branch))
        }
        val workspace = StandardWorktreeProvisioner().provision(
            WorkspaceProvisionRequest(root.resolve("confirmed-task"), repository, service, branch, setOf(conflict.key)),
        ).single()

        assertEquals(GitClient().resolve(seed, branch), GitClient().resolve(Path.of(workspace.worktreePath), "HEAD"))
        assertEquals("github/$branch", GitTestSupport.run(Path.of(workspace.worktreePath), "rev-parse", "--abbrev-ref", "@{upstream}"))
    }

    @Test
    fun `local repository remote catalog returns configured remote names`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("remote-catalog"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("remote-catalog/source"))
        GitTestSupport.run(repositoryPath, "remote", "add", "upstream", remote.toString())

        assertEquals(listOf("origin", "upstream"), GitRepositoryRemoteCatalog().list(repositoryPath))
    }

    @Test
    fun `ordinary workspace provisioning ignores conflicting local and remote tags`() {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("tag-conflict-create"))
        GitTestSupport.run(seed, "tag", "same-tag")
        GitTestSupport.run(seed, "push", "origin", "same-tag")
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("tag-conflict-create").resolve("service"))
        Files.writeString(seed.resolve("tag-move.txt"), "new\n")
        GitTestSupport.run(seed, "add", "tag-move.txt")
        GitTestSupport.run(seed, "commit", "-m", "move tag")
        GitTestSupport.run(seed, "tag", "-f", "same-tag")
        GitTestSupport.run(seed, "push", "--force", "origin", "refs/tags/same-tag")
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig.standard("tag-service", repository.id, "tag", masterBranch = "origin/master")

        val workspace = StandardWorktreeProvisioner().provision(
            WorkspaceProvisionRequest(temporary.resolve("tag-conflict-create").resolve("task"), repository, service, "feature/tag-safe"),
        ).single()

        assertEquals("feature/tag-safe", workspace.branch)
    }

    @Test
    fun `standard strategy creates one worktree for each base branch module`() {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("standard"))
        GitTestSupport.run(seed, "switch", "-c", "development")
        Files.writeString(seed.resolve("development.txt"), "development\n")
        GitTestSupport.run(seed, "add", "development.txt")
        GitTestSupport.run(seed, "commit", "-m", "development")
        GitTestSupport.run(seed, "push", "-u", "origin", "development")
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("standard").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig(
            id = "service-standard",
            repositoryId = repository.id,
            displayName = "订单服务",
            modules = listOf(
                ServiceModuleConfig("master", "master", "origin/master"),
                ServiceModuleConfig("development", "development", "origin/development"),
            ),
        )

        val workspaces = StandardWorktreeProvisioner().provision(
            WorkspaceProvisionRequest(
                taskDirectory = temporary.resolve("standard").resolve("task"),
                repository = repository,
                service = service,
                requestedFeatureBranch = "feature/OBT-123",
            ),
        )

        assertEquals(
            listOf("feature/OBT-123-master", "feature/OBT-123-development"),
            workspaces.map { it.branch },
        )
        assertTrue(workspaces.all { Files.isDirectory(Path.of(it.worktreePath)) })
    }

    @Test
    fun `standard strategy creates independent worktrees for modules on the same base branch`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("same-base"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("same-base/service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig(
            id = "same-base-service",
            repositoryId = repository.id,
            displayName = "same-base",
            modules = listOf(
                ServiceModuleConfig("api", "api", "origin/master"),
                ServiceModuleConfig("job", "job", "origin/master"),
            ),
        )

        val workspaces = StandardWorktreeProvisioner().provision(
            WorkspaceProvisionRequest(
                taskDirectory = temporary.resolve("same-base/task"),
                repository = repository,
                service = service,
                requestedFeatureBranch = "feature/OBT-123",
                moduleBranches = mapOf("api" to "feature/custom-api"),
            ),
        )

        assertEquals(listOf("feature/custom-api", "feature/OBT-123-job"), workspaces.map { it.branch })
        assertEquals(2, workspaces.map { Path.of(it.worktreePath) }.distinct().size)
        assertTrue(workspaces.all { Files.isDirectory(Path.of(it.worktreePath)) })
    }

    @Test
    fun `standard provisioning uses the shared common directory repository lock`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("provision-lock"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("provision-lock/service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig.standard("service", repository.id, "Service", masterBranch = "origin/master")
        val taskDirectory = temporary.resolve("provision-lock/task")
        val lock = RepositoryOperationLock(ApplicationPaths(temporary.resolve("provision-lock-home")))
        val provisioner = StandardWorktreeProvisioner(repositoryLock = lock)

        lock.withLock(GitClient().commonDirectory(repositoryPath)) {
            assertThrows(IllegalStateException::class.java) {
                provisioner.provision(WorkspaceProvisionRequest(taskDirectory, repository, service, "feature/locked"))
            }
        }

        assertTrue(Files.notExists(taskDirectory))
        assertEquals(false, GitClient().refExists(repositoryPath, "refs/heads/feature/locked"))
    }

    @Test
    fun `clone strategy checks out the task branch override without creating a linked worktree`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("clone"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("clone").resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig(
            id = "service-clone",
            repositoryId = repository.id,
            displayName = "复杂单仓",
            modules = listOf(ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = true)),
        )

        val workspace = IndependentCloneProvisioner().provision(
            WorkspaceProvisionRequest(
                taskDirectory = temporary.resolve("clone").resolve("task"),
                repository = repository,
                service = service,
            ),
        ).single()

        assertEquals("master", workspace.branch)
        assertEquals("origin/master", workspace.baseRef)
        assertEquals(WorkspaceStrategy.INDEPENDENT_CLONE, workspace.strategy)
        assertEquals("master", GitClient().currentBranch(Path.of(workspace.worktreePath)))
        assertEquals(1, GitClient().worktrees(Path.of(workspace.worktreePath)).size)
    }

    @Test
    fun `clone strategy uses the selected source remote but names it origin in the new clone`() {
        val root = temporary.resolve("clone-selected-source")
        val (originRemote, _) = GitTestSupport.createRemoteWithSeed(root.resolve("origin"))
        val (upstreamRemote, _) = GitTestSupport.createRemoteWithSeed(root.resolve("upstream"))
        val source = GitTestSupport.clone(originRemote, root.resolve("source"))
        GitTestSupport.run(source, "remote", "add", "upstream", upstreamRemote.toString())
        val repository = GitRepositoryInspector().inspect(source)
        val module = ServiceModuleConfig(
            id = "clone",
            strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
            masterBranch = "upstream/master",
            tagEnabled = false,
        )
        val service = GroupServiceConfig("service-clone", repository.id, "Clone", modules = listOf(module))

        val workspace = IndependentCloneProvisioner().provision(
            WorkspaceProvisionRequest(root.resolve("task"), repository, service),
        ).single()
        val target = Path.of(workspace.worktreePath)

        assertEquals("upstream/master", workspace.baseRef)
        assertEquals(upstreamRemote.toString(), workspace.originUrl)
        assertEquals("origin", workspace.pushRemote)
        assertEquals(upstreamRemote.toString(), GitClient().remoteUrl(target, "origin"))
        assertEquals("master", GitClient().currentBranch(target))
    }

    @Test
    fun `clone module creates an unpushed target from its base when target is absent`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("clone-target-new"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("clone-target-new/source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val module = ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = false)
        val service = GroupServiceConfig("service-clone", repository.id, "Clone", modules = listOf(module))

        val workspace = IndependentCloneProvisioner().provision(
            WorkspaceProvisionRequest(
                temporary.resolve("clone-target-new/task"),
                repository,
                service,
                moduleBranches = mapOf("clone" to "feature/new-clone"),
            ),
        ).single()

        val target = Path.of(workspace.worktreePath)
        assertEquals("feature/new-clone", GitClient().currentBranch(target))
        assertThrows(IllegalStateException::class.java) {
            GitTestSupport.run(target, "config", "branch.feature/new-clone.remote")
        }
    }

    @Test
    fun `failed clone initialization removes the clone directory created by this request`() {
        val root = temporary.resolve("clone-bootstrap-rollback")
        val (remote, _) = GitTestSupport.createRemoteWithSeed(root)
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val module = ServiceModuleConfig(
            id = "clone",
            strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
            masterBranch = "origin/master",
            tagEnabled = false,
        )
        val service = GroupServiceConfig(
            id = "service-clone",
            repositoryId = repository.id,
            displayName = "Clone",
            modules = listOf(module),
            bootstrap = BootstrapConfig(commands = listOf(BootstrapCommand("invalid", "git", timeoutSeconds = 0))),
        )
        val taskDirectory = root.resolve("task")
        val target = taskDirectory.resolve(WorkspaceLayout.moduleDirectoryName(service, module))

        assertThrows(IllegalArgumentException::class.java) {
            IndependentCloneProvisioner().provision(
                WorkspaceProvisionRequest(taskDirectory, repository, service),
            )
        }

        assertTrue(!Files.exists(target), "failed clone must be rolled back immediately")
    }

    @Test
    fun `mixed provisioning resolves multi module default targets before splitting requests`() {
        val root = temporary.resolve("mixed-default-targets")
        val (remote, _) = GitTestSupport.createRemoteWithSeed(root)
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig(
            id = "mixed-service",
            repositoryId = repository.id,
            displayName = "Mixed",
            modules = listOf(
                ServiceModuleConfig("api", name = "default", tagEnabled = false),
                ServiceModuleConfig("web", name = "web", tagEnabled = false),
            ),
        )

        val workspaces = WorkspaceProvisioningService().provision(
            WorkspaceProvisionRequest(
                taskDirectory = root.resolve("task"),
                repository = repository,
                service = service,
                requestedFeatureBranch = "feature/mixed",
            ),
        )

        assertEquals(listOf("feature/mixed-default", "feature/mixed-web"), workspaces.map(ServiceWorkspace::branch))
        assertEquals(listOf("default", "web"), workspaces.map(ServiceWorkspace::moduleName))
    }

    @Test
    fun `clone module requires confirmation before tracking an existing remote target`() {
        val root = temporary.resolve("clone-target-existing")
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root)
        GitTestSupport.run(seed, "switch", "-c", "feature/shared")
        GitTestSupport.run(seed, "push", "-u", "origin", "feature/shared")
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val module = ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = false)
        val service = GroupServiceConfig("service-clone", repository.id, "Clone", modules = listOf(module))
        val request = WorkspaceProvisionRequest(
            root.resolve("task"), repository, service,
            moduleBranches = mapOf("clone" to "feature/shared"),
        )
        val conflict = WorkspaceBranchReuseInspector().inspect(
            repository,
            service,
            requestedFeatureBranch = "",
            moduleBranches = request.moduleBranches,
        ).single()

        assertThrows(IllegalArgumentException::class.java) { IndependentCloneProvisioner().provision(request) }
        val workspace = IndependentCloneProvisioner().provision(
            request.copy(confirmedBranchReuseKeys = setOf(conflict.key)),
        ).single()

        assertEquals("feature/shared", workspace.branch)
        assertEquals("origin", GitTestSupport.run(Path.of(workspace.worktreePath), "config", "branch.feature/shared.remote"))
    }

    @Test
    fun `confirmed clone target deletion fails instead of creating a different branch history`() {
        val root = temporary.resolve("clone-target-race")
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root)
        GitTestSupport.run(seed, "switch", "-c", "feature/shared")
        GitTestSupport.run(seed, "push", "-u", "origin", "feature/shared")
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val module = ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = false)
        val service = GroupServiceConfig("service-clone", repository.id, "Clone", modules = listOf(module))
        val request = WorkspaceProvisionRequest(root.resolve("task"), repository, service, moduleBranches = mapOf("clone" to "feature/shared"))
        val conflict = WorkspaceBranchReuseInspector().inspect(repository, service, "", request.moduleBranches).single()
        GitTestSupport.run(seed, "push", "origin", "--delete", "feature/shared")

        val error = assertThrows(IllegalArgumentException::class.java) {
            IndependentCloneProvisioner().provision(request.copy(confirmedBranchReuseKeys = setOf(conflict.key)))
        }

        assertTrue(error.message.orEmpty().contains("状态已变化"))
        assertTrue(!Files.exists(root.resolve("task").resolve(WorkspaceLayout.moduleDirectoryName(service, module))))
    }

    @Test
    fun `clone target race never deletes a directory created by another process`() {
        val root = temporary.resolve("clone-target-directory-race")
        val (remote, _) = GitTestSupport.createRemoteWithSeed(root)
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val module = ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = false)
        val service = GroupServiceConfig("service-clone", repository.id, "Clone", modules = listOf(module))
        val taskDirectory = root.resolve("task")
        val target = taskDirectory.resolve(WorkspaceLayout.moduleDirectoryName(service, module))
        val marker = target.resolve("owned-by-external.txt")
        val delegate = ProcessCommandRunner()
        val injected = java.util.concurrent.atomic.AtomicBoolean()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: java.time.Duration, environment: Map<String, String>): CommandResult {
                if ("clone" in command && injected.compareAndSet(false, true)) {
                    Files.createDirectories(target)
                    Files.writeString(marker, "external")
                }
                return delegate.run(command, workingDirectory, timeout, environment)
            }
        }

        assertThrows(Exception::class.java) {
            IndependentCloneProvisioner(GitClient(runner)).provision(
                WorkspaceProvisionRequest(taskDirectory, repository, service, moduleBranches = mapOf("clone" to "")),
            )
        }

        assertEquals("external", Files.readString(marker))
    }

    @Test
    fun `clone rollback refuses a target directory replaced after provisioning`() {
        val root = temporary.resolve("clone-target-replaced-before-rollback")
        val (remote, _) = GitTestSupport.createRemoteWithSeed(root)
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val module = ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = false)
        val service = GroupServiceConfig("service-clone", repository.id, "Clone", modules = listOf(module))
        val request = WorkspaceProvisionRequest(root.resolve("task"), repository, service, moduleBranches = mapOf("clone" to ""))
        val provisioner = IndependentCloneProvisioner()
        val workspaces = provisioner.provision(request)
        val target = Path.of(workspaces.single().worktreePath)
        Files.move(target, target.resolveSibling("original-clone"))
        Files.createDirectories(target)
        val marker = target.resolve("owned-by-external.txt")
        Files.writeString(marker, "external")

        assertThrows(IllegalArgumentException::class.java) { provisioner.rollback(request, workspaces) }

        assertEquals("external", Files.readString(marker))
    }

    @Test
    fun `confirmed clone target movement requires a new confirmation`() {
        val root = temporary.resolve("clone-target-moved")
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root)
        GitTestSupport.run(seed, "switch", "-c", "feature/shared")
        GitTestSupport.run(seed, "push", "-u", "origin", "feature/shared")
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val module = ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = false)
        val service = GroupServiceConfig("service-clone", repository.id, "Clone", modules = listOf(module))
        val request = WorkspaceProvisionRequest(root.resolve("task"), repository, service, moduleBranches = mapOf("clone" to "feature/shared"))
        val conflict = WorkspaceBranchReuseInspector().inspect(repository, service, "", request.moduleBranches).single()
        Files.writeString(seed.resolve("moved.txt"), "moved")
        GitTestSupport.run(seed, "add", "moved.txt")
        GitTestSupport.run(seed, "commit", "-m", "move shared")
        GitTestSupport.run(seed, "push", "origin", "feature/shared")

        assertThrows(IllegalArgumentException::class.java) {
            IndependentCloneProvisioner().provision(request.copy(confirmedBranchReuseKeys = setOf(conflict.key)))
        }
    }

    @Test
    fun `confirmed remote worktree target movement requires a new confirmation`() {
        val root = temporary.resolve("worktree-target-moved")
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root)
        val branch = "feature/shared"
        GitTestSupport.run(seed, "switch", "-c", branch)
        GitTestSupport.run(seed, "push", "-u", "origin", branch)
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig.standard("service", repository.id, "Service")
        val conflict = WorkspaceBranchReuseInspector().inspect(repository, service, branch).single()
        Files.writeString(seed.resolve("moved.txt"), "moved")
        GitTestSupport.run(seed, "add", "moved.txt")
        GitTestSupport.run(seed, "commit", "-m", "move shared")
        GitTestSupport.run(seed, "push", "origin", branch)

        assertThrows(IllegalArgumentException::class.java) {
            StandardWorktreeProvisioner().provision(
                WorkspaceProvisionRequest(root.resolve("task"), repository, service, branch, setOf(conflict.key)),
            )
        }
    }

    @Test
    fun `standard strategy fetches and uses latest remote base without moving local master`() {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("latest-base"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("latest-base").resolve("service"))
        val localMasterBefore = GitClient().resolve(repositoryPath, "refs/heads/master")
        Files.writeString(seed.resolve("latest.txt"), "latest\n")
        GitTestSupport.run(seed, "add", "latest.txt")
        GitTestSupport.run(seed, "commit", "-m", "latest")
        GitTestSupport.run(seed, "push", "origin", "master")
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig.standard(
            id = "latest-service",
            repositoryId = repository.id,
            displayName = "latest",
            masterBranch = "origin/master",
        )

        val workspace = StandardWorktreeProvisioner().provision(
            WorkspaceProvisionRequest(
                temporary.resolve("latest-base").resolve("task"),
                repository,
                service,
                "feature/latest",
            ),
        ).single()

        assertTrue(Files.exists(Path.of(workspace.worktreePath).resolve("latest.txt")))
        assertEquals(localMasterBefore, GitClient().resolve(repositoryPath, "refs/heads/master"))
        assertEquals("origin/master", workspace.baseRef)
    }

    @Test
    fun `failed later module rolls back earlier worktree and task branches`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("rollback"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("rollback").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig(
            id = "rollback-service",
            repositoryId = repository.id,
            displayName = "rollback",
            modules = listOf(
                ServiceModuleConfig("master", "master", "origin/master"),
                ServiceModuleConfig("missing", "missing", "origin/does-not-exist"),
            ),
        )

        assertThrows(Throwable::class.java) {
            StandardWorktreeProvisioner().provision(
                WorkspaceProvisionRequest(
                    temporary.resolve("rollback").resolve("task"),
                    repository,
                    service,
                    "feature/rollback",
                ),
            )
        }

        assertEquals(1, GitClient().worktrees(repositoryPath).size)
        assertEquals(false, GitClient().refExists(repositoryPath, "refs/heads/feature/rollback-master"))
        assertEquals(false, GitClient().refExists(repositoryPath, "refs/heads/feature/rollback-does-not-exist"))
    }

    @Test
    fun `add worktree failure never deletes a branch created by another operation`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("existing-branch"))
        val repository = GitTestSupport.clone(remote, temporary.resolve("existing-branch").resolve("service"))
        GitTestSupport.run(repository, "branch", "feature/existing", "origin/master")

        assertThrows(Throwable::class.java) {
            GitClient().addWorktree(
                repository,
                temporary.resolve("existing-branch").resolve("task").resolve("service"),
                "feature/existing",
                "origin/master",
            )
        }

        assertTrue(GitClient().refExists(repository, "refs/heads/feature/existing"))
    }

    @Test
    fun `existing local branch needs confirmation then attaches without changing ownership`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("reuse-local"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("reuse-local").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val branch = "feature/reuse-local"
        GitTestSupport.run(repositoryPath, "branch", branch, "origin/master")
        val service = GroupServiceConfig.standard("reuse-local", repository.id, "reuse-local", masterBranch = "origin/master")
        val provisioner = StandardWorktreeProvisioner()

        assertThrows(Throwable::class.java) {
            provisioner.provision(
                WorkspaceProvisionRequest(temporary.resolve("reuse-local").resolve("unconfirmed"), repository, service, branch),
            )
        }
        assertTrue(GitClient().refExists(repositoryPath, "refs/heads/$branch"))

        val workspace = provisioner.provision(
            WorkspaceProvisionRequest(
                temporary.resolve("reuse-local").resolve("confirmed"),
                repository,
                service,
                branch,
                setOf(BranchReuseKey(repository.id, branch)),
            ),
        ).single()

        assertEquals(false, workspace.branchCreatedByTask)
        assertEquals(false, workspace.forceWorktreeAttach)
        assertEquals(branch, GitClient().currentBranch(Path.of(workspace.worktreePath)))
    }

    @Test
    fun `occupied branch requires force attach and can be attached again after removal`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("reuse-occupied"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("reuse-occupied").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val branch = "feature/reuse-occupied"
        GitTestSupport.run(repositoryPath, "branch", branch, "origin/master")
        val git = GitClient()
        git.addExistingWorktree(repositoryPath, temporary.resolve("reuse-occupied").resolve("existing"), branch)
        val service = GroupServiceConfig.standard("reuse-occupied", repository.id, "reuse-occupied", masterBranch = "origin/master")

        val workspace = StandardWorktreeProvisioner().provision(
            WorkspaceProvisionRequest(
                temporary.resolve("reuse-occupied").resolve("task"),
                repository,
                service,
                branch,
                setOf(BranchReuseKey(repository.id, branch)),
            ),
        ).single()

        assertTrue(workspace.forceWorktreeAttach)
        val target = Path.of(workspace.worktreePath)
        git.removeWorktree(repositoryPath, target, force = true)
        git.addExistingWorktree(repositoryPath, target, branch, force = workspace.forceWorktreeAttach)
        assertEquals(branch, git.currentBranch(target))
    }

    @Test
    fun `stale worktree registration is pruned before branch reuse inspection`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("reuse-stale"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("reuse-stale").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val branch = "feature/reuse-stale"
        GitTestSupport.run(repositoryPath, "branch", branch, "origin/master")
        val stale = temporary.resolve("reuse-stale").resolve("deleted-worktree")
        GitClient().addExistingWorktree(repositoryPath, stale, branch)
        Files.walk(stale).use { it.sorted(Comparator.reverseOrder()).forEach(Files::delete) }

        val service = GroupServiceConfig.standard("reuse-stale", repository.id, "reuse-stale", masterBranch = "origin/master")
        val conflicts = WorkspaceBranchReuseInspector().inspect(repository, service, branch)

        assertEquals(listOf(branch), conflicts.map { it.key.branch })
        assertTrue(conflicts.single().occupiedWorktreePaths.isEmpty())
        assertTrue(GitClient().worktrees(repositoryPath).none { it.branch == branch })
    }

    @Test
    fun `locked worktree is never force attached`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("reuse-locked"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("reuse-locked").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val branch = "feature/reuse-locked"
        GitTestSupport.run(repositoryPath, "branch", branch, "origin/master")
        val locked = temporary.resolve("reuse-locked").resolve("locked-worktree")
        GitClient().addExistingWorktree(repositoryPath, locked, branch)
        GitTestSupport.run(repositoryPath, "worktree", "lock", "--reason", "protected", locked.toString())
        val service = GroupServiceConfig.standard("reuse-locked", repository.id, "reuse-locked", masterBranch = "origin/master")

        val conflict = WorkspaceBranchReuseInspector().inspect(repository, service, branch).single()
        assertEquals(listOf(locked.toString()), conflict.lockedWorktreePaths)
        assertThrows(IllegalArgumentException::class.java) {
            StandardWorktreeProvisioner().provision(
                WorkspaceProvisionRequest(
                    temporary.resolve("reuse-locked").resolve("task"),
                    repository,
                    service,
                    branch,
                    setOf(BranchReuseKey(repository.id, branch)),
                ),
            )
        }
    }

    @Test
    fun `branch reuse confirmation is rejected when worktree state changes`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("reuse-changed"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("reuse-changed").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val branch = "feature/reuse-changed"
        GitTestSupport.run(repositoryPath, "branch", branch, "origin/master")
        val service = GroupServiceConfig.standard("reuse-changed", repository.id, "reuse-changed", masterBranch = "origin/master")
        val confirmation = WorkspaceBranchReuseInspector().inspect(repository, service, branch).single().key
        GitClient().addExistingWorktree(repositoryPath, temporary.resolve("reuse-changed").resolve("other"), branch)

        assertThrows(IllegalArgumentException::class.java) {
            StandardWorktreeProvisioner().provision(
                WorkspaceProvisionRequest(
                    temporary.resolve("reuse-changed").resolve("task"),
                    repository,
                    service,
                    branch,
                    setOf(confirmation),
                ),
            )
        }
    }

    @Test
    fun `remote-only branch needs confirmation and becomes a tracked local worktree`() {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("reuse-remote"))
        val branch = "feature/reuse-remote"
        GitTestSupport.run(seed, "switch", "-c", branch)
        GitTestSupport.run(seed, "push", "-u", "origin", branch)
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("reuse-remote").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val service = GroupServiceConfig.standard("reuse-remote", repository.id, "reuse-remote", masterBranch = "origin/master")

        val workspace = StandardWorktreeProvisioner().provision(
            WorkspaceProvisionRequest(
                temporary.resolve("reuse-remote").resolve("task"),
                repository,
                service,
                branch,
                setOf(BranchReuseKey(repository.id, branch)),
            ),
        ).single()

        assertTrue(workspace.branchCreatedByTask)
        assertEquals("origin", GitTestSupport.run(repositoryPath, "config", "branch.$branch.remote"))
        assertEquals("refs/heads/$branch", GitTestSupport.run(repositoryPath, "config", "branch.$branch.merge"))
    }

    @Test
    fun `failed provisioning never deletes a reused local branch`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("reuse-rollback"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("reuse-rollback").resolve("service"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val requestedBranch = "feature/reuse-rollback"
        val reusedBranch = "$requestedBranch-master"
        GitTestSupport.run(repositoryPath, "branch", reusedBranch, "origin/master")
        val service = GroupServiceConfig(
            id = "reuse-rollback",
            repositoryId = repository.id,
            displayName = "reuse-rollback",
            modules = listOf(
                ServiceModuleConfig("master", "master", "origin/master"),
                ServiceModuleConfig("missing", "missing", "origin/does-not-exist"),
            ),
        )

        assertThrows(Throwable::class.java) {
            StandardWorktreeProvisioner().provision(
                WorkspaceProvisionRequest(
                    temporary.resolve("reuse-rollback").resolve("task"),
                    repository,
                    service,
                    requestedBranch,
                    setOf(BranchReuseKey(repository.id, reusedBranch)),
                ),
            )
        }

        assertTrue(GitClient().refExists(repositoryPath, "refs/heads/$reusedBranch"))
        assertEquals(1, GitClient().worktrees(repositoryPath).size)
    }
}
