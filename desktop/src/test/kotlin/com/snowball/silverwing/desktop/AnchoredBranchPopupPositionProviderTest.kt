package com.snowball.silverwing.desktop

import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntRect
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.LayoutDirection
import kotlin.test.Test
import kotlin.test.assertEquals

class AnchoredBranchPopupPositionProviderTest {
    private val provider = AnchoredBranchPopupPositionProvider()

    @Test
    fun `popup opens below its field when enough space remains`() {
        assertEquals(
            IntOffset(100, 160),
            provider.calculatePosition(
                anchorBounds = IntRect(100, 100, 500, 160),
                windowSize = IntSize(1600, 900),
                layoutDirection = LayoutDirection.Ltr,
                popupContentSize = IntSize(720, 400),
            ),
        )
    }

    @Test
    fun `popup moves above and stays inside the window near an edge`() {
        assertEquals(
            IntOffset(880, 300),
            provider.calculatePosition(
                anchorBounds = IntRect(1300, 700, 1500, 760),
                windowSize = IntSize(1600, 900),
                layoutDirection = LayoutDirection.Ltr,
                popupContentSize = IntSize(720, 400),
            ),
        )
    }
}
