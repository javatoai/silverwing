package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AgentTaskTemplate
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Test

class TaskTemplateFillingTest {
    private fun template(id: String, content: String = "$id 内容") =
        AgentTaskTemplate(id, "模板$id", content, "2026-08-19 16:00:00")

    @Test
    fun `selecting a template fills blank notes directly`() {
        val result = resolveTemplateToggle("", null, template("a"))

        assertEquals(TemplateFillResult.Applied("a 内容", "a"), result)
    }

    @Test
    fun `selecting a template over handwritten notes requires confirmation`() {
        val result = resolveTemplateToggle("手写的说明", null, template("a"))

        assertEquals(TemplateFillResult.NeedsConfirmation(template("a")), result)
    }

    @Test
    fun `switching templates replaces untouched template content directly`() {
        val result = resolveTemplateToggle("a 内容", template("a"), template("b"))

        assertEquals(TemplateFillResult.Applied("b 内容", "b"), result)
    }

    @Test
    fun `switching templates after a manual edit requires confirmation`() {
        val result = resolveTemplateToggle("a 内容加手写补充", template("a"), template("b"))

        assertEquals(TemplateFillResult.NeedsConfirmation(template("b")), result)
    }

    @Test
    fun `deselecting clears notes that still equal the template content`() {
        val result = resolveTemplateToggle("a 内容", template("a"), template("a"))

        assertEquals(TemplateFillResult.Applied("", null), result)
    }

    @Test
    fun `deselecting keeps manually edited notes`() {
        val result = resolveTemplateToggle("a 内容加手写补充", template("a"), template("a"))

        assertEquals(TemplateFillResult.Applied("a 内容加手写补充", null), result)
    }

    @Test
    fun `existing notes preselect their unique matching template`() {
        assertEquals("a", selectedTemplateIdForNotes("a 内容", listOf(template("a"), template("b"))))
    }

    @Test
    fun `existing notes do not preselect an arbitrary template when contents duplicate`() {
        assertEquals(null, selectedTemplateIdForNotes("相同内容", listOf(template("a", "相同内容"), template("b", "相同内容"))))
    }
}
