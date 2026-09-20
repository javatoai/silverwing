package com.snowball.silverwing.desktop

import androidx.compose.material3.lightColorScheme
import androidx.compose.ui.graphics.Color
import kotlin.test.Test
import kotlin.test.assertEquals

class StatusColorTest {
    private val colors = lightColorScheme(
        primary = BrandBlue,
        error = DangerRed,
        outline = Color.Gray,
    )

    @Test
    fun `Chinese Tag operation labels preserve their status colors`() {
        assertEquals(SuccessGreen, colors.statusColor("构建成功"))
        assertEquals(DangerRed, colors.statusColor("构建失败"))
        assertEquals(DangerRed, colors.statusColor("存在冲突"))
        assertEquals(DangerRed, colors.statusColor("已创建"))
        assertEquals(DangerRed, colors.statusColor("预检通过"))
        assertEquals(DangerRed, colors.statusColor("源分支已推送"))
        assertEquals(WarningAmber, colors.statusColor("部分完成"))
        assertEquals(BrandBlue, colors.statusColor("本地Tag已创建"))
        assertEquals(BrandBlue, colors.statusColor("目标分支已推送"))
        assertEquals(BrandBlue, colors.statusColor("Tag已推送"))
    }

    @Test
    fun `legacy status labels retain their established colors`() {
        assertEquals(SuccessGreen, colors.statusColor("SUCCESS"))
        assertEquals(DangerRed, colors.statusColor("FAILED"))
        assertEquals(WarningAmber, colors.statusColor("PARTIAL"))
    }
}
