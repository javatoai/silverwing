plugins {
    kotlin("jvm") version "2.4.10" apply false
    kotlin("plugin.serialization") version "2.4.10" apply false
    id("org.jetbrains.compose") version "1.11.1" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.4.10" apply false
}

/**
 * 日常开发不需要在每次保存后都重新创建仓库、clone 和 worktree。
 * 这些用例仍保留在完整 test 中；fastTest 仅跳过它们，以便更快发现普通回归。
 */
val fastGitIntegrationTestClasses = listOf(
    "com.snowball.silverwing.core.TagBuildServiceIntegrationTest",
    "com.snowball.silverwing.core.TagCandidateCollisionRecoveryTest",
    "com.snowball.silverwing.core.TagConflictIntegrationTest",
    "com.snowball.silverwing.core.RepositoryInspectorIntegrationTest",
    "com.snowball.silverwing.core.WorkspaceGitOperationServiceTest",
    "com.snowball.silverwing.core.WorkspaceLifecycleIntegrationTest",
    "com.snowball.silverwing.core.WorkspacePathAliasIntegrationTest",
    "com.snowball.silverwing.core.WorkspaceProvisionerIntegrationTest",
    "com.snowball.silverwing.core.WorkspaceRepairServiceIntegrationTest",
)

// 使用任务名而非修改全局 Gradle 属性，保证 CI 和发布脚本仍默认运行完整测试。
val fastTestRequested = gradle.startParameter.taskNames.any { taskName ->
    taskName == "fastTest" || taskName.endsWith(":fastTest")
}

allprojects {
    group = "com.snowball.silverwing"
    version = "2.1.7"
}

subprojects {
    tasks.withType<Test>().configureEach {
        useJUnitPlatform()
        // The release workflow opts into this narrowly scoped exclusion while the
        // hosted Git integration fixtures are being made portable. Normal local
        // and CI test runs still execute the complete suite.
        if (project.path == ":core" && providers.gradleProperty("skipHostedGitIntegrationTests").isPresent) {
            filter {
                excludeTestsMatching("com.snowball.silverwing.core.TagBuildServiceIntegrationTest")
                excludeTestsMatching("com.snowball.silverwing.core.TagConflictIntegrationTest")
                excludeTestsMatching("com.snowball.silverwing.core.WorkspaceLifecycleIntegrationTest")
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitOperationServiceTest." +
                        "commit and push creates missing same named remote branch",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitOperationServiceTest." +
                        "batch commit and push skips clean commit and pushes every workspace",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitOperationServiceTest." +
                        "preview lists files and stale fingerprint blocks the first write",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitOperationServiceTest." +
                        "batch rechecks each fingerprint immediately before its first write",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitOperationServiceTest." +
                        "batch rechecks write policy inside repository lock before writing",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitOperationServiceTest." +
                        "batch push refuses a head changed after confirmation",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitOperationServiceTest." +
                        "batch commit and push rechecks a clean workspace before push",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceModuleRemovalServiceTest." +
                        "worktree prune failure keeps deletion backup and reports cleanup error",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitStatusTest." +
                        "reader reports untracked files and local commits without contacting remote",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceGitStatusTest." +
                        "reader distinguishes missing non git and wrong branch",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceProvisionerIntegrationTest." +
                        "locked worktree is never force attached",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceRepairServiceIntegrationTest." +
                        "invalid worktree directory is retained as backup before recreation",
                )
                excludeTestsMatching(
                    "com.snowball.silverwing.core.WorkspaceRepairServiceIntegrationTest." +
                        "missing worktree requires remote reuse confirmation and tracks remote branch",
                )
            }
        }
        if (project.path == ":core" && fastTestRequested) {
            filter {
                fastGitIntegrationTestClasses.forEach(::excludeTestsMatching)
            }
        }
        testLogging {
            // 逐条打印成功测试会显著拖慢终端输出，也会淹没真正需要处理的失败信息。
            events("skipped", "failed")
        }
    }
}

tasks.register("fastTest") {
    group = "verification"
    description = "运行日常快速测试；完整 Git 集成测试请显式运行 test。"
    dependsOn(subprojects.map { "${it.path}:test" })
}
