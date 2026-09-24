package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.FeishuWorkItemLink
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class CreateTaskLayoutTest {
    @Test
    fun `materials directory lines use compact spacing inside the task form`() {
        val layout = taskInformationLayout()

        assertEquals(11, layout.formItemSpacingDp)
        assertEquals(2, layout.materialsLineSpacingDp)
        assertTrue(layout.materialsLineSpacingDp < layout.formItemSpacingDp)
    }

    @Test
    fun `task name field reserves supporting space only when an error exists`() {
        assertNull(taskNameSupportingMessage(null))
        assertEquals("文件夹名称无效", taskNameSupportingMessage("文件夹名称无效"))
    }

    @Test
    fun `an absent requirement seed keeps the blank create form`() {
        val draft = initialCreateTaskDraft("feature/", null)

        assertEquals("", draft.requirementLink)
        assertNull(draft.requirementTitle)
        assertEquals("feature/", draft.branch)
        assertFalse(draft.metadataLoading)
    }

    @Test
    fun `a requirement seed fills link, title and resolved branch like a candidate click`() {
        val url = "https://project.feishu.cn/obt/userstory/detail/7049480011"
        val draft = initialCreateTaskDraft("feature/{num}-", CreateTaskRequirement("输出一份规范文档", url))

        assertEquals(url, draft.requirementLink)
        assertEquals("输出一份规范文档", draft.requirementTitle)
        assertEquals("feature/7049480011-", draft.branch)
        assertTrue(draft.metadataLoading)
        assertEquals("7049480011", FeishuWorkItemLink.parse(url)?.workItemId)
    }

    @Test
    fun `a blank seed title never replaces the fetched requirement title`() {
        val url = "https://project.feishu.cn/obt/othertask/detail/7101000342"
        val draft = initialCreateTaskDraft("feature/", CreateTaskRequirement("   ", url))

        assertNull(draft.requirementTitle)
        assertEquals(url, draft.requirementLink)
        assertTrue(draft.metadataLoading)
    }
}
