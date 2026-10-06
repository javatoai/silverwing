package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class WorkspaceGitFilePreviewServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `language detection covers supported workspace file types`() {
        assertEquals(WorkspaceFileLanguage.JAVA, WorkspaceFileLanguage.fromPath("src/Demo.java"))
        assertEquals(WorkspaceFileLanguage.KOTLIN, WorkspaceFileLanguage.fromPath("src/Demo.kt"))
        assertEquals(WorkspaceFileLanguage.GRADLE, WorkspaceFileLanguage.fromPath("build.gradle.kts"))
        assertEquals(WorkspaceFileLanguage.PROPERTIES, WorkspaceFileLanguage.fromPath("application.properties"))
        assertEquals(WorkspaceFileLanguage.HTML, WorkspaceFileLanguage.fromPath("page.html"))
        assertEquals(WorkspaceFileLanguage.CSS, WorkspaceFileLanguage.fromPath("site.css"))
        assertEquals(WorkspaceFileLanguage.JAVASCRIPT, WorkspaceFileLanguage.fromPath("app.js"))
        assertEquals(WorkspaceFileLanguage.TYPESCRIPT, WorkspaceFileLanguage.fromPath("app.tsx"))
        assertEquals(WorkspaceFileLanguage.JSON, WorkspaceFileLanguage.fromPath("config.json"))
        assertEquals(WorkspaceFileLanguage.YAML, WorkspaceFileLanguage.fromPath("config.yml"))
        assertEquals(WorkspaceFileLanguage.SQL, WorkspaceFileLanguage.fromPath("query.sql"))
        assertEquals(WorkspaceFileLanguage.PYTHON, WorkspaceFileLanguage.fromPath("scripts/RUN.PY"))
        assertEquals(WorkspaceFileLanguage.PYTHON, WorkspaceFileLanguage.fromPath("typing.pyi"))
        assertEquals(WorkspaceFileLanguage.BATCH, WorkspaceFileLanguage.fromPath("bootstrap.cmd"))
        assertEquals(WorkspaceFileLanguage.BATCH, WorkspaceFileLanguage.fromPath("bootstrap.bat"))
        assertEquals(WorkspaceFileLanguage.SHELL, WorkspaceFileLanguage.fromPath("bootstrap.sh"))
        assertEquals(WorkspaceFileLanguage.POWERSHELL, WorkspaceFileLanguage.fromPath("bootstrap.ps1"))
        assertEquals(WorkspaceFileLanguage.MARKDOWN, WorkspaceFileLanguage.fromPath("README.md"))
        assertEquals(WorkspaceFileLanguage.PLAIN_TEXT, WorkspaceFileLanguage.fromPath("NOTICE"))
    }

    @Test
    fun `modified preview aligns staged and unstaged changes against head with line numbers`() {
        val repository = repositoryWithSeed()
        val source = repository.resolve("src/Demo.java")
        Files.createDirectories(source.parent)
        Files.writeString(
            source,
            """
            package sample;
            class Demo {
                String version = "old";
                int count = 1;
            }
            """.trimIndent() + "\n",
        )
        commitAll(repository, "seed source")

        Files.writeString(
            source,
            """
            package sample;
            class Demo {
                String version = "staged";
                int count = 1;
            }
            """.trimIndent() + "\n",
        )
        GitTestSupport.run(repository, "add", "src/Demo.java")
        Files.writeString(
            source,
            """
            package sample;
            class Demo {
                String version = "staged";
                int count = 2;
                boolean enabled = true;
            }
            """.trimIndent() + "\n",
        )

        val preview = WorkspaceGitFilePreviewService().preview(
            repository.toString(),
            WorkspaceGitFileChange("src/Demo.java", WorkspaceGitFileChangeKind.MODIFIED),
        )

        assertEquals(WorkspaceFileLanguage.JAVA, preview.language)
        assertEquals(WorkspaceFilePreviewMode.COMPARISON, preview.defaultMode)
        assertContains(assertNotNull(preview.content).text, "boolean enabled = true;")
        val comparison = assertNotNull(preview.comparison)
        assertEquals(WorkspaceFileContentOrigin.HEAD, assertNotNull(comparison.oldContent).origin)
        assertEquals(WorkspaceFileContentOrigin.WORKTREE, assertNotNull(comparison.newContent).origin)
        assertTrue(
            comparison.rows.any {
                it.oldLine?.kind == WorkspaceFileComparisonLineKind.DELETED &&
                    it.oldLine.text == "    String version = \"old\";" &&
                    it.oldLine.lineNumber == 3 &&
                    it.newLine?.kind == WorkspaceFileComparisonLineKind.ADDED &&
                    it.newLine.text == "    String version = \"staged\";" &&
                    it.newLine.lineNumber == 3
            },
        )
        assertTrue(
            comparison.rows.any {
                it.oldLine?.text == "    int count = 1;" &&
                    it.newLine?.text == "    int count = 2;" &&
                    it.oldLine.kind == WorkspaceFileComparisonLineKind.DELETED &&
                    it.newLine.kind == WorkspaceFileComparisonLineKind.ADDED
            },
        )
        assertTrue(
            comparison.rows.any {
                it.oldLine == null &&
                    it.newLine?.kind == WorkspaceFileComparisonLineKind.ADDED &&
                    it.newLine.text == "    boolean enabled = true;" &&
                    it.newLine.lineNumber == 5
            },
        )
        assertTrue(comparison.rows.any { !it.isChanged && it.oldLine?.lineNumber == 1 && it.newLine?.lineNumber == 1 })
    }

    @Test
    fun `new untracked and deleted files provide single-sided comparisons`() {
        val repository = repositoryWithSeed()
        val untracked = repository.resolve("runtime.properties")
        Files.writeString(untracked, "feature.enabled=true\nretry.count=3\n")

        val untrackedPreview = WorkspaceGitFilePreviewService().preview(
            repository.toString(),
            WorkspaceGitFileChange("runtime.properties", WorkspaceGitFileChangeKind.UNTRACKED),
        )
        assertEquals(WorkspaceFileLanguage.PROPERTIES, untrackedPreview.language)
        assertEquals(WorkspaceFilePreviewMode.COMPARISON, untrackedPreview.defaultMode)
        val untrackedComparison = assertNotNull(untrackedPreview.comparison)
        assertNull(untrackedComparison.oldContent)
        assertEquals(listOf(1, 2), untrackedComparison.rows.mapNotNull { it.newLine?.lineNumber })
        assertTrue(untrackedComparison.rows.all { it.oldLine == null && it.newLine?.kind == WorkspaceFileComparisonLineKind.ADDED })

        val staged = repository.resolve("staged.json")
        Files.writeString(staged, "{\"enabled\": true}\n")
        GitTestSupport.run(repository, "add", "staged.json")
        val stagedPreview = WorkspaceGitFilePreviewService().preview(
            repository.toString(),
            WorkspaceGitFileChange("staged.json", WorkspaceGitFileChangeKind.ADDED),
        )
        assertTrue(assertNotNull(stagedPreview.comparison).rows.all { it.oldLine == null && it.newLine?.kind == WorkspaceFileComparisonLineKind.ADDED })

        val deleted = repository.resolve("obsolete.html")
        Files.writeString(deleted, "<h1>obsolete</h1>\n")
        commitAll(repository, "seed deleted file")
        Files.delete(deleted)

        val deletedPreview = WorkspaceGitFilePreviewService().preview(
            repository.toString(),
            WorkspaceGitFileChange("obsolete.html", WorkspaceGitFileChangeKind.DELETED),
        )
        val deletedContent = assertNotNull(deletedPreview.content)
        assertEquals(WorkspaceFileContentOrigin.HEAD, deletedContent.origin)
        assertContains(deletedContent.text, "obsolete")
        val deletedComparison = assertNotNull(deletedPreview.comparison)
        assertNull(deletedComparison.newContent)
        assertTrue(deletedComparison.rows.any { it.oldLine?.kind == WorkspaceFileComparisonLineKind.DELETED && it.oldLine.lineNumber == 1 && it.newLine == null })
    }

    @Test
    fun `preview safely handles binary large and path escaping inputs`() {
        val repository = repositoryWithSeed()
        val binary = repository.resolve("asset.bin")
        Files.write(binary, byteArrayOf(0, 1, 2))
        val binaryPreview = WorkspaceGitFilePreviewService().preview(
            repository.toString(),
            WorkspaceGitFileChange("asset.bin", WorkspaceGitFileChangeKind.UNTRACKED),
        )
        assertNull(binaryPreview.content)
        assertContains(assertNotNull(binaryPreview.unavailableReason), "二进制")

        val large = repository.resolve("large.txt")
        Files.writeString(large, "a".repeat(512 * 1024 + 8))
        val largePreview = WorkspaceGitFilePreviewService().preview(
            repository.toString(),
            WorkspaceGitFileChange("large.txt", WorkspaceGitFileChangeKind.UNTRACKED),
        )
        assertTrue(assertNotNull(largePreview.content).truncated)
        assertNull(largePreview.comparison)
        assertContains(assertNotNull(largePreview.notice), "较大")

        assertFailsWith<IllegalArgumentException> {
            WorkspaceGitFilePreviewService().preview(
                repository.toString(),
                WorkspaceGitFileChange("../outside.txt", WorkspaceGitFileChangeKind.MODIFIED),
            )
        }
    }

    @Test
    fun `renamed preview falls back to the current text content`() {
        val repository = repositoryWithSeed()
        val source = repository.resolve("source.md")
        Files.writeString(source, "# original\n")
        commitAll(repository, "seed rename")
        Files.move(source, repository.resolve("renamed.md"))

        val preview = WorkspaceGitFilePreviewService().preview(
            repository.toString(),
            WorkspaceGitFileChange("renamed.md", WorkspaceGitFileChangeKind.RENAMED),
        )

        assertContains(assertNotNull(preview.content).text, "original")
        assertContains(assertNotNull(preview.notice), "重命名")
        assertNull(preview.comparison)
    }

    private fun repositoryWithSeed(): Path {
        val repository = temporary.resolve("repository")
        Files.createDirectories(repository)
        GitTestSupport.run(temporary, "init", repository.toString())
        GitTestSupport.run(repository, "symbolic-ref", "HEAD", "refs/heads/master")
        GitTestSupport.configureIdentity(repository)
        Files.writeString(repository.resolve("README.md"), "seed\n")
        commitAll(repository, "seed")
        return repository
    }

    private fun commitAll(repository: Path, message: String) {
        GitTestSupport.run(repository, "add", "-A")
        GitTestSupport.run(repository, "commit", "-m", message)
    }
}
