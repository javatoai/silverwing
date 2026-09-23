package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.LocalSkillFileEntry
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs

class LocalSkillsTreeTest {
    @Test
    fun `builds a folder first tree with root Skill markdown pinned first`() {
        val tree = buildLocalSkillFileTree(
            skillDirectoryName = "release-check",
            files = listOf(
                file("scripts/verify/check.ps1"),
                file("README.md"),
                file("references/checklist.md", markdown = true),
                file("SKILL.md", markdown = true),
                file("agents/openai.yaml"),
                file("scripts/run.ps1"),
            ),
        )

        assertEquals(
            listOf(
                "file:SKILL.md",
                "directory:agents",
                "directory:references",
                "directory:scripts",
                "file:README.md",
            ),
            tree.children.map(::nodeKey),
        )
    }

    @Test
    fun `only root content is visible until folders are expanded and file paths remain intact`() {
        val tree = buildLocalSkillFileTree(
            skillDirectoryName = "release-check",
            files = listOf(
                file("SKILL.md", markdown = true),
                file("references/checklist.md", markdown = true),
                file("scripts/verify/check.ps1"),
                file("scripts/run.ps1"),
            ),
        )

        assertEquals(
            listOf("directory:", "file:SKILL.md", "directory:references", "directory:scripts"),
            visibleLocalSkillFileTreeRows(tree, emptySet()).map(LocalSkillFileTreeRow::key),
        )

        val expanded = visibleLocalSkillFileTreeRows(tree, setOf("references", "scripts", "scripts/verify"))
        assertEquals(
            listOf(
                "directory:",
                "file:SKILL.md",
                "directory:references",
                "file:references/checklist.md",
                "directory:scripts",
                "directory:scripts/verify",
                "file:scripts/verify/check.ps1",
                "file:scripts/run.ps1",
            ),
            expanded.map(LocalSkillFileTreeRow::key),
        )
        val checklist = assertIs<LocalSkillFileTreeNode.File>(expanded.single { it.key == "file:references/checklist.md" }.node)
        assertEquals("references/checklist.md", checklist.entry.relativePath)
    }

    private fun file(path: String, markdown: Boolean = false) = LocalSkillFileEntry(path, sizeBytes = 10, markdown = markdown)

    private fun nodeKey(node: LocalSkillFileTreeNode): String = when (node) {
        is LocalSkillFileTreeNode.Directory -> "directory:${node.relativePath}"
        is LocalSkillFileTreeNode.File -> "file:${node.entry.relativePath}"
    }
}
