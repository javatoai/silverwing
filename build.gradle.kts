plugins {
    kotlin("jvm") version "2.4.10" apply false
    kotlin("plugin.serialization") version "2.4.10" apply false
    id("org.jetbrains.compose") version "1.11.1" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.4.10" apply false
}

allprojects {
    group = "com.snowball.silverwing"
    version = "2.0.3"
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
        testLogging {
            events("passed", "skipped", "failed")
        }
    }
}
