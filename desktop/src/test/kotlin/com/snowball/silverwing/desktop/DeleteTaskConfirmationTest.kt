package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertContains

class DeleteTaskConfirmationTest {
    @Test
    fun `permanent task deletion warns about every external file holder`() {
        val warning = deleteTaskExternalWindowWarning()

        assertContains(warning, "Codex")
        assertContains(warning, "IDE")
        assertContains(warning, "文件管理器")
        assertContains(warning, "占用")
        assertContains(warning, "不会自动关闭")
    }
}
