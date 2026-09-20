package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
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
}
