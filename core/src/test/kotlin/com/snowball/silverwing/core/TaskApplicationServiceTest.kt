package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class TaskApplicationServiceTest {
    @Test
    fun `new task snapshots resolved master branch while later service changes do not alter it`() {
        val root = Files.createTempDirectory("task-master-branch-")
        val provisioner = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(provisioner)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )
        val original = taskConfig(root)
        val group = original.groups.single()
        val standard = group.services.first { it.id == "standard" }
        val inherited = original.copy(groups = listOf(group.copy(services = listOf(
            standard.copy(masterBranch = "upstream/develop"),
        ))))

        val first = application.create(inherited, CreateGroupedTaskRequest(
            folderName = "FIRST",
            featureBranch = "feature/first",
            groupId = "alpha",
            serviceIds = listOf("standard"),
        ))
        assertEquals("upstream/develop", first.services.single().baseRef)
        assertEquals("upstream", first.services.single().pushRemote)

        val changed = original.copy(groups = listOf(group.copy(services = listOf(
            standard.copy(masterBranch = "origin/main"),
        ))))
        val second = application.create(changed, CreateGroupedTaskRequest(
            folderName = "SECOND",
            featureBranch = "feature/second",
            groupId = "alpha",
            serviceIds = listOf("standard"),
        ))
        assertEquals("origin/main", second.services.single().baseRef)
        assertEquals("upstream/develop", ManifestStore().load(root.resolve("FIRST")).services.single().baseRef)
        assertEquals("upstream/develop", provisioner.requests.first().service.effectiveMasterBranch(provisioner.requests.first().service.modules.single()))
    }

    @Test
    fun `new task snapshots service test baseline or module override`() {
        val root = Files.createTempDirectory("task-tag-baseline-")
        val provisioner = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(provisioner)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )
        val original = taskConfig(root)
        val group = original.groups.single()
        val standard = group.services.first { it.id == "standard" }
        val inherited = original.copy(groups = listOf(group.copy(services = listOf(
            standard.copy(testTagBaselineRef = "origin/qa"),
        ))))
        val first = application.create(inherited, CreateGroupedTaskRequest(
            folderName = "INHERITED",
            featureBranch = "feature/inherited",
            groupId = "alpha",
            serviceIds = listOf("standard"),
        ))
        assertEquals("origin/qa", provisioner.requests.last().service.modules.single().tagTargetRef)
        assertEquals("origin/qa", first.services.single().tagTargetRef)

        val changedBaseline = inherited.copy(groups = listOf(group.copy(services = listOf(
            standard.copy(testTagBaselineRef = "origin/release/test"),
        ))))
        assertEquals("qa", TagPolicy.resolve(changedBaseline, first, first.services.single().selectionKey).targetBranch)

        val override = inherited.copy(groups = listOf(group.copy(services = listOf(
            standard.copy(testTagBaselineRef = "origin/qa", modules = listOf(
                standard.modules.single().copy(tagTargetRef = "origin/hotfix"),
            )),
        ))))
        val second = application.create(override, CreateGroupedTaskRequest(
            folderName = "OVERRIDE",
            featureBranch = "feature/override",
            groupId = "alpha",
            serviceIds = listOf("standard"),
        ))
        assertEquals("origin/hotfix", second.services.single().tagTargetRef)
    }

    @Test
    fun `Agent CLI context creates a handoff while a normal request remains opt in`() {
        val root = Files.createTempDirectory("agent-handoff-task-")
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE))),
            agentDocuments = AgentDocumentService(ApplicationPaths(root.resolve("silverwing-home"))),
            operationLock = NoOpTaskOperationLock,
        )

        application.create(
            taskConfig(root.resolve("tasks")),
            CreateGroupedTaskRequest(
                folderName = "AGENT-HANDOFF",
                featureBranch = "feature/agent-handoff",
                groupId = "alpha",
                serviceIds = listOf("standard"),
                agentContext = AgentTaskContext(
                    documentationDirectory = root.resolve("docs").toString(),
                    iterationLabel = "OBT-20260817--20260828",
                ),
                agentHandoffMarkdown = "api_key: should-be-redacted",
            ),
        )

        val taskDirectory = root.resolve("tasks").resolve("AGENT-HANDOFF")
        assertTrue(Files.readString(taskDirectory.resolve("AGENTS.md")).startsWith("# 任务说明"))
        assertTrue(
            Files.readString(
                taskDirectory.resolve(".workspace").resolve("agent").resolve("TASK-CONTEXT.md"),
            ).contains("任务交接（仅 Agent CLI 创建）"),
        )
        val handoff = Files.readString(taskDirectory.resolve(".workspace").resolve("HANDOFF.md"))
        assertTrue(handoff.contains("[REDACTED]"))
        assertTrue(!handoff.contains("should-be-redacted"))
    }

    @Test
    fun `create rejects an unsafe task directory name before provisioning`() {
        val root = Files.createTempDirectory("unsafe-task-name-")
        val service = TaskApplicationService(operationLock = NoOpTaskOperationLock)

        assertFailsWith<IllegalArgumentException> {
            service.create(
                taskConfig(root),
                CreateGroupedTaskRequest(
                    folderName = "unsafe:name",
                    featureBranch = "feature/unsafe-name",
                    groupId = "alpha",
                    serviceIds = listOf("standard"),
                ),
            )
        }
        Files.list(root).use { children ->
            assertTrue(!children.findAny().isPresent)
        }
    }

    @Test
    fun `task belongs to selected group and delegates each service to its strategy`() {
        val root = Files.createTempDirectory("task-app-")
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val clone = RecordingProvisioner(WorkspaceStrategy.INDEPENDENT_CLONE)
        val documents = RecordingAgentDocuments()
        val config = taskConfig(root)
        val service = TaskApplicationService(
            manifests = ManifestStore(),
            provisioning = WorkspaceProvisioningService(listOf(standard, clone)),
            agentDocuments = documents,
            operationLock = NoOpTaskOperationLock,
            clock = Clock.fixed(Instant.parse("2026-08-08T00:00:00Z"), ZoneOffset.UTC),
        )

        val manifest = service.create(
            config,
            CreateGroupedTaskRequest(
                folderName = "TASK-20",
                featureBranch = "feature/task-20",
                groupId = "alpha",
                serviceIds = listOf("standard", "clone"),
                requirementLink = "https://example.test/task/20",
                taskNotes = "only edit the API module",
                baseOverrides = listOf(
                    ModuleBaseOverride("standard", "default", "upstream/develop", "feature/custom-standard"),
                    ModuleBaseOverride("clone", "clone", "origin/release/test"),
                ),
            ),
        )

        assertEquals("alpha", manifest.groupId)
        assertEquals(listOf(WorkspaceStrategy.STANDARD_WORKTREE), standard.requests.map { it.service.modules.single().strategy })
        assertEquals("upstream/develop", standard.requests.single().service.effectiveMasterBranch(standard.requests.single().service.modules.single()))
        assertEquals("upstream", standard.requests.single().service.effectiveMasterRemote(standard.requests.single().service.modules.single()))
        assertEquals(mapOf("default" to "feature/custom-standard"), standard.requests.single().moduleBranches)
        assertEquals("origin/release/test", clone.requests.single().service.effectiveMasterBranch(clone.requests.single().service.modules.single()))
        assertEquals(2, manifest.services.size)
        assertEquals("2026-08-08 08:00:00", manifest.createdAt)
        assertEquals("2026-08-08 08:00:00", manifest.updatedAt)
        assertEquals(AgentDocumentLayout.REFERENCED_V2, manifest.agentDocumentLayout)
        assertEquals("only edit the API module", documents.lastNotes)
        assertTrue(Files.exists(root.resolve("TASK-20").resolve(ManifestStore.FILE_NAME)))
    }

    @Test
    fun `unconfigured materials directory is not requested during creation`() {
        val taskRoot = Files.createTempDirectory("task-materials-")
        val provisioner = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val documents = RecordingAgentDocuments()
        val manifests = ManifestStore()
        val application = TaskApplicationService(
            manifests = manifests,
            provisioning = WorkspaceProvisioningService(listOf(provisioner)),
            agentDocuments = documents,
            requirementMaterials = RequirementMaterialsService(),
            operationLock = NoOpTaskOperationLock,
        )
        val unconfigured = taskConfig(taskRoot)

        val created = application.create(
            unconfigured,
            CreateGroupedTaskRequest(
                folderName = "支付优化",
                featureBranch = "feature/7035269559",
                groupId = "alpha",
                serviceIds = listOf("standard"),
                requirementLink = "7035269559",
            ),
        )

        assertEquals("7035269559", created.requirementId)
        assertEquals(RequirementMaterialsStatus.NOT_REQUESTED, created.requirementMaterials.status)
        assertEquals(null, created.requirementMaterials.writeRoot)
        assertEquals(null, created.requirementMaterials.failureReason)
        assertTrue(Files.exists(taskRoot.resolve("支付优化").resolve(ManifestStore.FILE_NAME)))
    }

    @Test
    fun `a not requested materials directory can be resolved after settings are configured`() {
        val taskRoot = Files.createTempDirectory("task-materials-retry-")
        val materialsRoot = Files.createTempDirectory("requirement-materials-")
        val existingRequirement = materialsRoot.resolve("Sprint A").resolve("7035269559-existing")
        Files.createDirectories(existingRequirement)
        val manifests = ManifestStore()
        val application = TaskApplicationService(
            manifests = manifests,
            provisioning = WorkspaceProvisioningService(listOf(RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE))),
            agentDocuments = RecordingAgentDocuments(),
            requirementMaterials = RequirementMaterialsService(),
            operationLock = NoOpTaskOperationLock,
        )

        application.create(
            taskConfig(taskRoot),
            CreateGroupedTaskRequest(
                folderName = "支付优化",
                featureBranch = "feature/7035269559",
                groupId = "alpha",
                serviceIds = listOf("standard"),
                requirementLink = "7035269559",
            ),
        )

        val retried = application.retryRequirementMaterials(
            taskConfig(taskRoot).copy(
                requirementMaterialsRoot = materialsRoot.toString(),
                requirementMaterialsSubdirectory = "研发资料",
            ),
            taskRoot.resolve("支付优化"),
        )

        assertEquals(RequirementMaterialsStatus.READY, retried.requirementMaterials.status)
        assertEquals(existingRequirement.resolve("研发资料").toString(), retried.requirementMaterials.writeRoot)
        assertTrue(Files.isDirectory(existingRequirement.resolve("研发资料")))
        assertEquals(retried.requirementMaterials, manifests.load(taskRoot.resolve("支付优化")).requirementMaterials)
    }

    @Test
    fun `deleting a git task does not delete its independent materials directory`() {
        val taskRoot = Files.createTempDirectory("task-delete-materials-")
        val taskDirectory = Files.createDirectories(taskRoot.resolve("task"))
        val materialsWriteRoot = Files.createDirectories(taskRoot.resolveSibling("requirement-materials").resolve("Sprint").resolve("123-task").resolve("研发"))
        val preserved = Files.writeString(materialsWriteRoot.resolve("notes.md"), "keep")
        val manifests = ManifestStore()
        manifests.save(
            taskDirectory,
            TaskManifest(
                folderName = "task",
                taskDirectoryName = "task",
                featureBranch = "feature/task",
                requirementLink = "123",
                requirementId = "123",
                requirementMaterials = RequirementMaterialsDirectory(
                    status = RequirementMaterialsStatus.READY,
                    writeRoot = materialsWriteRoot.toString(),
                ),
                createdAt = "2026-08-08 00:00:00",
                updatedAt = "2026-08-08 00:00:00",
                lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                services = emptyList(),
            ),
        )
        val application = TaskApplicationService(
            manifests = manifests,
            lifecycle = DeletingTaskDirectoryLifecycle(),
            operationLock = NoOpTaskOperationLock,
        )

        application.delete(AppConfig(taskRoot = taskRoot.toString()), taskDirectory)

        assertTrue(!Files.exists(taskDirectory))
        assertEquals("keep", Files.readString(preserved))
    }

    @Test
    fun `task selection supports mixed dynamic modules and an empty clone target`() {
        val root = Files.createTempDirectory("task-mixed-selection-")
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val clone = RecordingProvisioner(WorkspaceStrategy.INDEPENDENT_CLONE)
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(standard, clone)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        val manifest = application.create(
            taskConfig(root),
            CreateGroupedTaskRequest(
                folderName = "TASK-MIXED",
                featureBranch = "feature/mixed",
                groupId = "alpha",
                serviceIds = listOf("standard"),
                serviceSelections = listOf(
                    TaskServiceSelection(
                        "standard",
                        listOf(
                            TaskModuleSelection("default", "default", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "feature/mixed-default"),
                            TaskModuleSelection("docs", "docs", WorkspaceStrategy.INDEPENDENT_CLONE, "origin/master", targetBranch = "", source = TaskModuleSource.TEMPORARY),
                        ),
                    ),
                ),
            ),
        )

        assertEquals(listOf(WorkspaceStrategy.STANDARD_WORKTREE), standard.requests.map { it.service.modules.single().strategy })
        assertEquals(listOf(WorkspaceStrategy.INDEPENDENT_CLONE), clone.requests.map { it.service.modules.single().strategy })
        assertEquals("", clone.requests.single().moduleBranches.getValue("docs"))
        assertEquals(2, manifest.services.size)
    }

    @Test
    fun `create rejects module directories colliding across different services`() {
        val root = Files.createTempDirectory("task-directory-collision-")
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val clone = RecordingProvisioner(WorkspaceStrategy.INDEPENDENT_CLONE)
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(standard, clone)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )
        val config = AppConfig(
            taskRoot = root.toString(),
            repositories = listOf(
                RepositoryConfig("repo-a", "A", "C:/repo-a", "C:/repo-a/.git", "https://example.test/a.git"),
                RepositoryConfig("repo-b", "B", "C:/repo-b", "C:/repo-b/.git", "https://example.test/b.git"),
            ),
            groups = listOf(
                GroupConfig(
                    "alpha",
                    "Alpha",
                    services = listOf(
                        GroupServiceConfig.standard("service-a", "repo-a", "Shared"),
                        GroupServiceConfig.standard("service-b", "repo-b", "shared"),
                    ),
                ),
            ),
        )

        val error = assertFailsWith<IllegalArgumentException> {
            application.create(
                config,
                CreateGroupedTaskRequest("TASK-COLLISION", "feature/collision", "alpha", listOf("service-a", "service-b")),
            )
        }

        assertTrue(error.message.orEmpty().contains("工作区目录"))
        assertTrue(standard.requests.isEmpty())
        assertTrue(!Files.exists(root.resolve("TASK-COLLISION")))
    }

    @Test
    fun `create and preview reject default and explicit ancestor branches before provisioning`() {
        val root = Files.createTempDirectory("task-branch-ancestor-")
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(standard)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )
        val original = taskConfig(root)
        val configured = original.groupService("alpha", "standard").copy(
            modules = listOf(
                ServiceModuleConfig("release", "release"),
                ServiceModuleConfig("mobile", "release/mobile"),
            ),
        )
        val config = original.copy(groups = listOf(original.group("alpha").copy(services = listOf(configured))))
        val defaultRequest = CreateGroupedTaskRequest("TASK", "feature/task", "alpha", listOf("standard"))
        val explicitRequest = defaultRequest.copy(
            serviceSelections = listOf(TaskServiceSelection("standard", listOf(
                TaskModuleSelection("api", "api", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "feature/release/mobile"),
                TaskModuleSelection("job", "job", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "FEATURE/release"),
            ))),
        )

        listOf(defaultRequest, explicitRequest).forEach { request ->
            val previewError = assertFailsWith<IllegalArgumentException> { application.inspectCreateBranchReuse(config, request) }
            val createError = assertFailsWith<IllegalArgumentException> { application.create(config, request) }
            assertTrue(previewError.message.orEmpty().contains("父子路径"))
            assertTrue(createError.message.orEmpty().contains("父子路径"))
        }
        assertTrue(standard.requests.isEmpty())
        assertTrue(!Files.exists(root.resolve("TASK")))
    }

    @Test
    fun `branches in different repositories may have an ancestor relationship`() {
        val root = Files.createTempDirectory("task-branch-separate-repositories-")
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(standard)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )
        val original = taskConfig(root)
        val config = original.copy(groups = listOf(original.group("alpha").copy(services = listOf(
            GroupServiceConfig.standard("standard", "repo-a", "Repo A"),
            GroupServiceConfig.standard("other", "repo-b", "Repo B"),
        ))))

        val manifest = application.create(config, CreateGroupedTaskRequest(
            "TASK", "feature/task", "alpha", listOf("standard", "other"),
            serviceSelections = listOf(
                TaskServiceSelection("standard", listOf(TaskModuleSelection("default", "default", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "feature/release"))),
                TaskServiceSelection("other", listOf(TaskModuleSelection("default", "default", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "feature/release/mobile"))),
            ),
        ))

        assertEquals(listOf("feature/release", "feature/release/mobile"), manifest.services.map { it.targetBranch })
        assertEquals(2, standard.requests.size)
    }

    @Test
    fun `adding a module rejects ancestors of existing repository branches before mutation`() {
        val root = Files.createTempDirectory("task-add-branch-ancestor-")
        val taskDirectory = root.resolve("TASK")
        val store = ManifestStore()
        val existing = warningWorkspace(taskDirectory.resolve("Repo-A-existing"), WorkspaceHealth.READY, emptyList()).copy(
            moduleId = "existing",
            moduleName = "existing",
            branch = "feature/release/mobile",
            targetBranch = "feature/release/mobile",
        )
        val original = emptyManifest(TaskLifecycleStatus.ACTIVE).copy(groupId = "alpha", services = listOf(existing))
        store.save(taskDirectory, original)
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val application = TaskApplicationService(
            manifests = store,
            provisioning = WorkspaceProvisioningService(listOf(standard)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )
        val request = AddTaskModulesRequest("standard", listOf(
            TaskModuleSelection("new", "new", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "FEATURE/release"),
        ))

        val previewError = assertFailsWith<IllegalArgumentException> {
            application.inspectAddModulesBranchReuse(taskConfig(root), taskDirectory, request)
        }
        val createError = assertFailsWith<IllegalArgumentException> { application.addModules(taskConfig(root), taskDirectory, request) }

        assertTrue(previewError.message.orEmpty().contains("父子路径"))
        assertTrue(createError.message.orEmpty().contains("父子路径"))
        assertTrue(standard.requests.isEmpty())
        assertEquals(original, store.load(taskDirectory))
    }

    @Test
    fun `adding a module rejects existing module name and directory aliases`() {
        val root = Files.createTempDirectory("task-add-module-name-")
        val taskDirectory = root.resolve("TASK")
        val store = ManifestStore()
        store.save(
            taskDirectory,
            TaskManifest(
                folderName = "TASK",
                taskDirectoryName = "TASK",
                featureBranch = "feature/task",
                createdAt = "2026-08-13 08:00:00",
                updatedAt = "2026-08-13 08:00:00",
                groupId = "alpha",
                services = listOf(
                    ServiceWorkspace(
                        repositoryId = "repo-a",
                        serviceName = "Repo A",
                        repositoryPath = "C:/repo-a",
                        worktreePath = taskDirectory.resolve("Repo-A-api-core").toString(),
                        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                        branch = "feature/task-api-core",
                        groupServiceId = "standard",
                        moduleId = "existing",
                        moduleName = "api/core",
                        baseRef = "origin/master",
                        targetBranch = "feature/task-api-core",
                    ),
                    ServiceWorkspace(
                        repositoryId = "repo-b",
                        serviceName = "Repo B",
                        repositoryPath = "C:/repo-b",
                        worktreePath = taskDirectory.resolve("Repo-B-keep").toString(),
                        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                        branch = "feature/task-keep",
                        groupServiceId = "clone",
                        moduleId = "keep",
                        moduleName = "keep",
                        strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                        originUrl = "https://example.test/b.git",
                        baseRef = "origin/master",
                    ),
                ),
            ),
        )
        val application = TaskApplicationService(manifests = store, operationLock = NoOpTaskOperationLock)
        val sameName = AddTaskModulesRequest(
            "standard",
            listOf(TaskModuleSelection("new-id", "API/CORE", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "feature/new")),
        )
        val sameDirectory = AddTaskModulesRequest(
            "standard",
            listOf(TaskModuleSelection("other-id", "api-core", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "feature/other")),
        )

        assertFailsWith<IllegalArgumentException> { application.inspectAddModulesBranchReuse(taskConfig(root), taskDirectory, sameName) }
        assertFailsWith<IllegalArgumentException> { application.inspectAddModulesBranchReuse(taskConfig(root), taskDirectory, sameDirectory) }
    }

    @Test
    fun `adding a module rejects a directory used by another service`() {
        val root = Files.createTempDirectory("task-add-cross-service-directory-")
        val taskDirectory = root.resolve("TASK")
        val store = ManifestStore()
        val config = taskConfig(root)
        store.save(
            taskDirectory,
            TaskManifest(
                folderName = "TASK",
                taskDirectoryName = "TASK",
                featureBranch = "feature/task",
                createdAt = "2026-08-13 08:00:00",
                updatedAt = "2026-08-13 08:00:00",
                groupId = "alpha",
                services = listOf(
                    ServiceWorkspace(
                        repositoryId = "repo-b",
                        serviceName = "Other",
                        repositoryPath = "C:/repo-b",
                        worktreePath = taskDirectory.resolve("Repo-A-new").toString(),
                        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                        branch = "feature/other",
                        groupServiceId = "clone",
                        moduleId = "other",
                        moduleName = "other",
                        strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                        originUrl = "https://example.test/b.git",
                        baseRef = "origin/master",
                    ),
                    ServiceWorkspace(
                        repositoryId = "repo-a",
                        serviceName = "Repo A",
                        repositoryPath = "C:/repo-a",
                        worktreePath = taskDirectory.resolve("Repo-A-existing").toString(),
                        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                        branch = "feature/existing",
                        groupServiceId = "standard",
                        moduleId = "existing",
                        moduleName = "existing",
                        baseRef = "origin/master",
                        targetBranch = "feature/existing",
                    ),
                ),
            ),
        )
        val application = TaskApplicationService(manifests = store, operationLock = NoOpTaskOperationLock)
        val request = AddTaskModulesRequest(
            "standard",
            listOf(TaskModuleSelection("new", "new", WorkspaceStrategy.STANDARD_WORKTREE, "origin/master", targetBranch = "feature/new")),
        )

        assertFailsWith<IllegalArgumentException> {
            application.inspectAddModulesBranchReuse(config, taskDirectory, request)
        }
    }

    @Test
    fun `adding a module compares names with the task snapshot rather than stale service defaults`() {
        val root = Files.createTempDirectory("task-add-renamed-module-")
        val taskDirectory = root.resolve("TASK")
        val store = ManifestStore()
        val config = taskConfig(root)
        store.save(
            taskDirectory,
            TaskManifest(
                folderName = "TASK",
                taskDirectoryName = "TASK",
                featureBranch = "feature/task",
                createdAt = "2026-08-13 08:00:00",
                updatedAt = "2026-08-13 08:00:00",
                groupId = "alpha",
                services = listOf(
                    ServiceWorkspace(
                        repositoryId = "repo-a",
                        serviceName = "Repo A",
                        repositoryPath = "C:/repo-a",
                        worktreePath = taskDirectory.resolve("Repo-A-custom").toString(),
                        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                        branch = "feature/custom",
                        groupServiceId = "standard",
                        moduleId = "default",
                        moduleName = "custom",
                        baseRef = "origin/master",
                        targetBranch = "feature/custom",
                    ),
                ),
            ),
        )
        val provisioner = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val application = TaskApplicationService(
            manifests = store,
            provisioning = WorkspaceProvisioningService(listOf(provisioner)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        val updated = application.addModules(
            config,
            taskDirectory,
            AddTaskModulesRequest(
                "standard",
                listOf(
                    TaskModuleSelection(
                        id = "new-default",
                        name = "default",
                        strategy = WorkspaceStrategy.STANDARD_WORKTREE,
                        baseRef = "origin/master",
                        targetBranch = "feature/default",
                        source = TaskModuleSource.TEMPORARY,
                    ),
                ),
            ),
        )

        assertEquals(listOf("custom", "default"), updated.services.map(ServiceWorkspace::moduleName))
    }

    @Test
    fun `create rolls back completed services through their provisioning strategy`() {
        val root = Files.createTempDirectory("task-create-rollback-")
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val failingClone = RecordingProvisioner(WorkspaceStrategy.INDEPENDENT_CLONE, fail = true)
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(standard, failingClone)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        assertFailsWith<IllegalStateException> {
            application.create(
                taskConfig(root),
                CreateGroupedTaskRequest("TASK-ROLLBACK", "feature/rollback", "alpha", listOf("standard", "clone")),
            )
        }

        assertEquals(1, standard.rollbackCalls)
        assertTrue(!Files.exists(root.resolve("TASK-ROLLBACK")))
    }

    @Test
    fun `create removes generated referenced agent files when writing them fails`() {
        val root = Files.createTempDirectory("task-agent-document-rollback-")
        val application = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE))),
            agentDocuments = FailingAfterWritingAgentDocuments(
                AgentDocumentService(ApplicationPaths(root.resolve("silverwing-home"))),
            ),
            operationLock = NoOpTaskOperationLock,
        )

        assertFailsWith<IllegalStateException> {
            application.create(
                taskConfig(root),
                CreateGroupedTaskRequest("TASK-AGENT-ROLLBACK", "feature/agent-rollback", "alpha", listOf("standard")),
            )
        }

        assertFalse(Files.exists(root.resolve("TASK-AGENT-ROLLBACK")))
    }

    @Test
    fun `startup snapshot loads current files without repository inspection`() {
        val root = Files.createTempDirectory("startup-")
        val paths = ApplicationPaths(root.resolve("home"))
        val config = taskConfig(root.resolve("tasks"))
        ConfigStore(paths).save(config)
        val loader = StartupSnapshotLoader(ConfigStore(paths), ManifestStore())

        val snapshot = loader.load()

        assertEquals(config.repositories, snapshot.config.repositories)
        assertTrue(snapshot.tasks.isEmpty())
    }

    @Test
    fun `adds only services from the task group and rewrites agents document`() {
        val root = Files.createTempDirectory("task-add-services-")
        val taskDirectory = root.resolve("TASK-20")
        val manifests = ManifestStore()
        val original = TaskManifest(
            folderName = "TASK-20",
            taskDirectoryName = "TASK-20",
            featureBranch = "feature/task-20",
            createdAt = "2026-08-08 08:00:00",
            updatedAt = "2026-08-08 08:00:00",
            lifecycleStatus = TaskLifecycleStatus.ACTIVE,
            services = emptyList(),
            groupId = "alpha",
        )
        manifests.save(taskDirectory, original)
        val clone = RecordingProvisioner(WorkspaceStrategy.INDEPENDENT_CLONE)
        val documents = RecordingAgentDocuments()
        val application = TaskApplicationService(
            manifests = manifests,
            provisioning = WorkspaceProvisioningService(listOf(clone)),
            agentDocuments = documents,
            operationLock = NoOpTaskOperationLock,
            clock = Clock.fixed(Instant.parse("2026-08-08T01:02:03Z"), ZoneOffset.UTC),
        )

        val updated = application.addServices(
            taskConfig(root),
            taskDirectory,
            AddGroupedTaskServicesRequest(
                serviceIds = listOf("clone"),
            ),
        )

        assertEquals(listOf("clone"), updated.services.map(ServiceWorkspace::groupServiceId))
        assertEquals("feature/task-20", clone.requests.single().requestedFeatureBranch)
        assertEquals("origin/master", clone.requests.single().service.effectiveMasterBranch(clone.requests.single().service.modules.single()))
        assertEquals("2026-08-08 09:02:03", updated.updatedAt)
        assertEquals(null, documents.lastNotes)
    }

    @Test
    fun `add services rolls back earlier additions when a later service fails`() {
        val root = Files.createTempDirectory("task-add-rollback-")
        val taskDirectory = root.resolve("TASK-20")
        val manifests = ManifestStore()
        val original = TaskManifest(
            folderName = "TASK-20",
            taskDirectoryName = "TASK-20",
            featureBranch = "feature/task-20",
            createdAt = "2026-08-08 08:00:00",
            updatedAt = "2026-08-08 08:00:00",
            lifecycleStatus = TaskLifecycleStatus.ACTIVE,
            services = emptyList(),
            groupId = "alpha",
        )
        manifests.save(taskDirectory, original)
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val failingClone = RecordingProvisioner(WorkspaceStrategy.INDEPENDENT_CLONE, fail = true)
        val application = TaskApplicationService(
            manifests = manifests,
            provisioning = WorkspaceProvisioningService(listOf(standard, failingClone)),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        assertFailsWith<IllegalStateException> {
            application.addServices(
                taskConfig(root),
                taskDirectory,
                AddGroupedTaskServicesRequest(listOf("standard", "clone")),
            )
        }

        assertEquals(1, standard.rollbackCalls)
        assertEquals(original, manifests.load(taskDirectory))
    }

    @Test
    fun `add services restores manifest and workspaces when agents regeneration fails`() {
        val root = Files.createTempDirectory("task-add-agent-failure-")
        val taskDirectory = root.resolve("TASK-20")
        val manifests = ManifestStore()
        val original = TaskManifest(
            folderName = "TASK-20",
            taskDirectoryName = "TASK-20",
            featureBranch = "feature/task-20",
            createdAt = "2026-08-08 08:00:00",
            updatedAt = "2026-08-08 08:00:00",
            lifecycleStatus = TaskLifecycleStatus.ACTIVE,
            services = emptyList(),
            groupId = "alpha",
        )
        manifests.save(taskDirectory, original)
        val standard = RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE)
        val application = TaskApplicationService(
            manifests = manifests,
            provisioning = WorkspaceProvisioningService(listOf(standard)),
            agentDocuments = RecordingAgentDocuments(failOnFirstWrite = true),
            operationLock = NoOpTaskOperationLock,
        )

        assertFailsWith<IllegalStateException> {
            application.addServices(
                taskConfig(root),
                taskDirectory,
                AddGroupedTaskServicesRequest(listOf("standard")),
            )
        }

        assertEquals(1, standard.rollbackCalls)
        assertEquals(original, manifests.load(taskDirectory))
    }

    @Test
    fun `existing task directory is never reused or deleted by failed creation`() {
        val root = Files.createTempDirectory("task-existing-")
        val existing = root.resolve("TASK-20")
        Files.createDirectories(existing)
        val userFile = existing.resolve("keep.txt")
        Files.writeString(userFile, "keep")
        val service = TaskApplicationService(
            provisioning = WorkspaceProvisioningService(listOf(RecordingProvisioner(WorkspaceStrategy.STANDARD_WORKTREE))),
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        assertFailsWith<IllegalArgumentException> {
            service.create(
                taskConfig(root).copy(
                    groups = listOf(
                        GroupConfig(
                            "alpha",
                            "Alpha",
                            services = listOf(GroupServiceConfig.standard("standard", "repo-a", "Repo A")),
                        ),
                    ),
                ),
                CreateGroupedTaskRequest("TASK-20", "feature/x", "alpha", listOf("standard")),
            )
        }
        assertEquals("keep", Files.readString(userFile))
    }

    @Test
    fun `tampered clone path outside task directory is refused`() {
        val root = Files.createTempDirectory("task-delete-")
        val taskDirectory = root.resolve("task")
        val outside = root.resolve("outside")
        Files.createDirectories(taskDirectory)
        Files.createDirectories(outside)
        Files.writeString(outside.resolve("keep.txt"), "keep")
        ManifestStore().save(
            taskDirectory,
            TaskManifest(
                folderName = "task",
                taskDirectoryName = "task",
                featureBranch = "feature/x",
                createdAt = "2026-08-08T00:00:00Z",
                updatedAt = "2026-08-08T00:00:00Z",
                lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                services = listOf(
                    ServiceWorkspace(
                        repositoryId = "repo-a",
                        serviceName = "clone",
                        repositoryPath = outside.toString(),
                        worktreePath = outside.toString(),
                        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                        branch = "master",
                        strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                        originUrl = "https://example.test/a.git",
                    ),
                ),
            ),
        )

        assertFailsWith<IllegalArgumentException> {
            TaskApplicationService(operationLock = NoOpTaskOperationLock)
                .delete(taskConfig(root), taskDirectory, forceDiscard = true)
        }
        assertEquals("keep", Files.readString(outside.resolve("keep.txt")))
    }

    @Test
    fun `archive and restore do not mutate physical workspaces when manifest commit fails`() {
        val root = Files.createTempDirectory("task-commit-compensation-")
        val taskDirectory = root.resolve("task")
        Files.createDirectories(taskDirectory)
        val ready = emptyManifest(TaskLifecycleStatus.ACTIVE)
        val archiveLifecycle = RecordingLifecycle()
        val archiveApp = TaskApplicationService(
            manifests = FailingSaveManifests(ready),
            agentDocuments = RecordingAgentDocuments(),
            lifecycle = archiveLifecycle,
            operationLock = NoOpTaskOperationLock,
        )
        val config = AppConfig(taskRoot = root.toString())

        assertFailsWith<IllegalStateException> { archiveApp.archive(config, taskDirectory) }
        assertEquals(0, archiveLifecycle.removeCalls)
        assertEquals(0, archiveLifecycle.restoreCalls)

        val restoreLifecycle = RecordingLifecycle()
        val restoreApp = TaskApplicationService(
            manifests = FailingSaveManifests(emptyManifest(TaskLifecycleStatus.ARCHIVED)),
            agentDocuments = RecordingAgentDocuments(),
            lifecycle = restoreLifecycle,
            operationLock = NoOpTaskOperationLock,
        )
        assertFailsWith<IllegalStateException> { restoreApp.restore(config, taskDirectory) }
        assertEquals(0, restoreLifecycle.restoreCalls)
        assertEquals(0, restoreLifecycle.removeCalls)
    }

    @Test
    fun `clearing workspace warnings restores ready health only for that workspace`() {
        val root = Files.createTempDirectory("task-clear-warnings-")
        val taskDirectory = root.resolve("TASK")
        val store = ManifestStore()
        val warned = taskDirectory.resolve("wt-warned")
        val failed = taskDirectory.resolve("wt-failed")
        store.save(
            taskDirectory,
            TaskManifest(
                folderName = "TASK",
                taskDirectoryName = "TASK",
                featureBranch = "feature/task",
                createdAt = "2026-08-13 08:00:00",
                updatedAt = "2026-08-13 08:00:00",
                groupId = "alpha",
                services = listOf(
                    warningWorkspace(warned, WorkspaceHealth.READY_WITH_WARNINGS, listOf("执行 初始化 失败（退出码 1）")),
                    warningWorkspace(failed, WorkspaceHealth.FAILED, emptyList(), serviceId = "clone"),
                ),
            ),
        )
        val application = TaskApplicationService(
            manifests = store,
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        val updated = application.clearWorkspaceWarnings(taskConfig(root), taskDirectory, warned.toString())

        val cleared = updated.services.first { it.worktreePath == warned.toString() }
        val untouched = updated.services.first { it.worktreePath == failed.toString() }
        assertEquals(emptyList(), cleared.warnings)
        assertEquals(WorkspaceHealth.READY, cleared.health)
        assertEquals(WorkspaceHealth.FAILED, untouched.health)
        assertEquals(WorkspaceHealth.FAILED, updated.health)
        assertEquals(updated, store.load(taskDirectory))
    }

    @Test
    fun `rerun bootstrap replaces warnings with the fresh result`() {
        val root = Files.createTempDirectory("task-rerun-bootstrap-")
        val taskDirectory = root.resolve("TASK")
        val repository = root.resolve("repo-a")
        val worktree = taskDirectory.resolve("wt")
        Files.createDirectories(repository)
        Files.createDirectories(worktree)
        Files.writeString(repository.resolve("seed.txt"), "seed")
        val store = ManifestStore()
        store.save(
            taskDirectory,
            TaskManifest(
                folderName = "TASK",
                taskDirectoryName = "TASK",
                featureBranch = "feature/task",
                createdAt = "2026-08-13 08:00:00",
                updatedAt = "2026-08-13 08:00:00",
                groupId = "alpha",
                services = listOf(
                    warningWorkspace(worktree, WorkspaceHealth.READY_WITH_WARNINGS, listOf("旧警告"), repositoryPath = repository),
                ),
            ),
        )
        val config = taskConfig(root).copy(
            groups = listOf(
                GroupConfig(
                    "alpha",
                    "Alpha",
                    services = listOf(
                        GroupServiceConfig.standard("standard", "repo-a", "Repo A").copy(
                            bootstrap = BootstrapConfig(copyRules = listOf(BootstrapCopyRule("seed.txt", "copied.txt"))),
                        ),
                    ),
                ),
            ),
        )
        val application = TaskApplicationService(
            manifests = store,
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        val updated = application.rerunWorkspaceBootstrap(config, taskDirectory, worktree.toString())

        val entry = updated.services.single()
        assertEquals(emptyList(), entry.warnings)
        assertEquals(WorkspaceHealth.READY, entry.health)
        assertEquals("seed", Files.readString(worktree.resolve("copied.txt")))
    }

    @Test
    fun `rerun bootstrap keeps the fresh warnings when a step still fails`() {
        val root = Files.createTempDirectory("task-rerun-failing-")
        val taskDirectory = root.resolve("TASK")
        val repository = root.resolve("repo-a")
        val worktree = taskDirectory.resolve("wt")
        Files.createDirectories(repository)
        Files.createDirectories(worktree)
        val store = ManifestStore()
        store.save(
            taskDirectory,
            TaskManifest(
                folderName = "TASK",
                taskDirectoryName = "TASK",
                featureBranch = "feature/task",
                createdAt = "2026-08-13 08:00:00",
                updatedAt = "2026-08-13 08:00:00",
                groupId = "alpha",
                services = listOf(
                    warningWorkspace(worktree, WorkspaceHealth.READY_WITH_WARNINGS, listOf("旧警告"), repositoryPath = repository),
                ),
            ),
        )
        val config = taskConfig(root).copy(
            groups = listOf(
                GroupConfig(
                    "alpha",
                    "Alpha",
                    services = listOf(
                        GroupServiceConfig.standard("standard", "repo-a", "Repo A").copy(
                            bootstrap = BootstrapConfig(
                                commands = listOf(BootstrapCommand(name = "缺失命令", executable = "definitely-missing-silverwing-test-executable")),
                            ),
                        ),
                    ),
                ),
            ),
        )
        val application = TaskApplicationService(
            manifests = store,
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        val updated = application.rerunWorkspaceBootstrap(config, taskDirectory, worktree.toString())

        val entry = updated.services.single()
        assertEquals(WorkspaceHealth.READY_WITH_WARNINGS, entry.health)
        assertTrue(entry.warnings.any { it.contains("缺失命令") })
        assertTrue(entry.warnings.none { it.contains("旧警告") })
    }

    @Test
    fun `rerun bootstrap refuses a failed workspace`() {
        val root = Files.createTempDirectory("task-rerun-failed-")
        val taskDirectory = root.resolve("TASK")
        val worktree = taskDirectory.resolve("wt")
        Files.createDirectories(worktree)
        val store = ManifestStore()
        store.save(
            taskDirectory,
            TaskManifest(
                folderName = "TASK",
                taskDirectoryName = "TASK",
                featureBranch = "feature/task",
                createdAt = "2026-08-13 08:00:00",
                updatedAt = "2026-08-13 08:00:00",
                groupId = "alpha",
                services = listOf(warningWorkspace(worktree, WorkspaceHealth.FAILED, listOf("创建失败"))),
            ),
        )
        val application = TaskApplicationService(
            manifests = store,
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        assertFailsWith<IllegalArgumentException> {
            application.rerunWorkspaceBootstrap(taskConfig(root), taskDirectory, worktree.toString())
        }
    }

    @Test
    fun `rerun bootstrap reports a removed service configuration`() {
        val root = Files.createTempDirectory("task-rerun-missing-service-")
        val taskDirectory = root.resolve("TASK")
        val worktree = taskDirectory.resolve("wt")
        Files.createDirectories(worktree)
        val store = ManifestStore()
        store.save(
            taskDirectory,
            TaskManifest(
                folderName = "TASK",
                taskDirectoryName = "TASK",
                featureBranch = "feature/task",
                createdAt = "2026-08-13 08:00:00",
                updatedAt = "2026-08-13 08:00:00",
                groupId = "alpha",
                services = listOf(
                    warningWorkspace(worktree, WorkspaceHealth.READY_WITH_WARNINGS, listOf("旧警告"), serviceId = "ghost"),
                ),
            ),
        )
        val application = TaskApplicationService(
            manifests = store,
            agentDocuments = RecordingAgentDocuments(),
            operationLock = NoOpTaskOperationLock,
        )

        val error = assertFailsWith<IllegalStateException> {
            application.rerunWorkspaceBootstrap(taskConfig(root), taskDirectory, worktree.toString())
        }
        assertTrue(error.message.orEmpty().contains("服务配置已经不存在"))
    }
}

private fun warningWorkspace(
    path: Path,
    health: WorkspaceHealth,
    warnings: List<String>,
    serviceId: String = "standard",
    repositoryPath: Path? = null,
) = ServiceWorkspace(
    repositoryId = "repo-a",
    serviceName = "Repo A",
    repositoryPath = (repositoryPath ?: Path.of("C:/repo-a")).toString(),
    worktreePath = path.toString(),
    developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
    branch = "feature/task",
    health = health,
    warnings = warnings,
    groupServiceId = serviceId,
    moduleId = "default",
    moduleName = "default",
)

private fun emptyManifest(status: TaskLifecycleStatus) = TaskManifest(
    folderName = "task",
    taskDirectoryName = "task",
    featureBranch = "feature/task",
    createdAt = "2026-08-08T00:00:00Z",
    updatedAt = "2026-08-08T00:00:00Z",
    lifecycleStatus = status,
    services = emptyList(),
)

private class FailingSaveManifests(private val manifest: TaskManifest) : TaskManifestRepository {
    override fun save(taskDirectory: Path, manifest: TaskManifest) = error("manifest disk full")
    override fun load(taskDirectory: Path): TaskManifest = manifest
    override fun scan(taskRoot: Path) = ManifestScanResult(emptyList(), emptyList())
}

private class RecordingLifecycle : WorkspaceLifecycle {
    var removeCalls = 0
    var restoreCalls = 0
    override fun inspectDeleteRisks(config: AppConfig, taskDirectory: Path, manifest: TaskManifest) = emptyList<DeleteRisk>()
    override fun requireArchiveSafe(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean) = Unit
    override fun removeAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean): WorkspaceRemovalResult {
        removeCalls++
        return WorkspaceRemovalResult()
    }
    override fun restoreAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<ServiceWorkspace> {
        restoreCalls++
        return manifest.services
    }
    override fun validateForMutation(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
    ) = WorkspaceMutationTarget(Path.of(workspace.repositoryPath), Path.of(workspace.worktreePath))
}

private class DeletingTaskDirectoryLifecycle : WorkspaceLifecycle {
    override fun inspectDeleteRisks(config: AppConfig, taskDirectory: Path, manifest: TaskManifest) = emptyList<DeleteRisk>()
    override fun requireArchiveSafe(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean) = Unit
    override fun removeAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean): WorkspaceRemovalResult {
        Files.walk(taskDirectory).use { paths -> paths.sorted(Comparator.reverseOrder()).forEach(Files::deleteIfExists) }
        return WorkspaceRemovalResult()
    }
    override fun restoreAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest) = manifest.services
    override fun validateForMutation(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
    ) = WorkspaceMutationTarget(Path.of(workspace.repositoryPath), Path.of(workspace.worktreePath))
}

private fun taskConfig(taskRoot: Path): AppConfig {
    val repositories = listOf(
        RepositoryConfig("repo-a", "Repo A", "C:/repo-a", "C:/repo-a/.git", "https://example.test/a.git"),
        RepositoryConfig("repo-b", "Repo B", "C:/repo-b", "C:/repo-b/.git", "https://example.test/b.git"),
    )
    return AppConfig(
        taskRoot = taskRoot.toString(),
        repositories = repositories,
        groups = listOf(
            GroupConfig(
                id = "alpha",
                name = "Alpha",
                services = listOf(
                    GroupServiceConfig.standard("standard", "repo-a", "Repo A"),
                    GroupServiceConfig(
                        id = "clone",
                        repositoryId = "repo-b",
                        displayName = "Repo B",
                        modules = listOf(ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master")),
                    ),
                ),
            ),
        ),
    )
}

private class RecordingProvisioner(
    override val strategy: WorkspaceStrategy,
    private val fail: Boolean = false,
) : WorkspaceProvisioner {
    val requests = mutableListOf<WorkspaceProvisionRequest>()
    var rollbackCalls = 0

    override fun provision(request: WorkspaceProvisionRequest): List<ServiceWorkspace> {
        requests += request
        if (fail) error("provision failed")
        return listOf(
            ServiceWorkspace(
                repositoryId = request.repository.id,
                serviceName = request.service.displayName,
                repositoryPath = request.repository.rootPath,
                worktreePath = request.taskDirectory.resolve(request.service.id).toString(),
                developmentTool = request.service.developmentTool,
                branch = if (strategy == WorkspaceStrategy.INDEPENDENT_CLONE) request.service.effectiveMasterBranch(request.service.modules.first()).removePrefix("origin/") else request.requestedFeatureBranch.orEmpty(),
                health = WorkspaceHealth.READY,
                groupServiceId = request.service.id,
                moduleId = request.service.modules.single().id,
                moduleName = request.service.modules.single().name,
                strategy = strategy,
                baseRef = request.service.effectiveMasterBranch(request.service.modules.single()),
                targetBranch = request.moduleBranches[request.service.modules.single().id],
                tagEnabled = request.service.modules.single().tagEnabled,
                tagMode = request.service.modules.single().tagMode,
                tagTargetRef = request.service.modules.single().tagTargetRef,
                tagMessagePrefix = request.service.modules.single().tagMessagePrefix,
                pushRemote = request.service.effectiveMasterRemote(request.service.modules.single()),
            ),
        )
    }

    override fun rollback(request: WorkspaceProvisionRequest, workspaces: List<ServiceWorkspace>) {
        rollbackCalls++
    }
}

private class RecordingAgentDocuments(
    private val failOnFirstWrite: Boolean = false,
) : AgentDocuments {
    var lastNotes: String? = null
    var lastManifest: TaskManifest? = null
    private var writes = 0
    override fun readGlobal(): String = ""
    override fun saveGlobal(content: String) = Unit
    override fun readGroup(groupId: String): String = ""
    override fun saveGroup(groupId: String, content: String) = Unit
    override fun writeTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
    ): Path {
        writes++
        if (failOnFirstWrite && writes == 1) error("agents disk full")
        lastNotes = taskNotes
        lastManifest = manifest
        return taskDirectory.resolve("AGENTS.md")
    }
}

private class FailingAfterWritingAgentDocuments(
    private val delegate: AgentDocumentService,
) : AgentDocuments by delegate {
    override fun createTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
    ): Path {
        delegate.createTaskDocument(taskDirectory, manifest, repositories, taskNotes)
        error("agents disk full")
    }

    override fun writeTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
    ): Path {
        delegate.writeTaskDocument(taskDirectory, manifest, repositories, taskNotes)
        error("agents disk full")
    }
}
