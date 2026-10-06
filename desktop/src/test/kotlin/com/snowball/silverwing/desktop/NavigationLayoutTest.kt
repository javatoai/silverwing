package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class NavigationLayoutTest {
    @Test
    fun `uses the compact rail below the comfortable full sidebar width`() {
        assertEquals(NavigationLayout.COMPACT, navigationLayoutFor(COMPACT_NAVIGATION_MAX_WIDTH_DP - 1f))
        assertEquals(NavigationLayout.EXPANDED, navigationLayoutFor(COMPACT_NAVIGATION_MAX_WIDTH_DP))
    }

    @Test
    fun `each navigation presentation has a stable sidebar width`() {
        assertEquals(128f, sidebarWidthFor(NavigationLayout.EXPANDED))
        assertEquals(72f, sidebarWidthFor(NavigationLayout.COMPACT))
    }

    @Test
    fun `all page titles are removed while non task destinations retain a compact top gutter`() {
        assertEquals(0f, navigationContentTopPaddingFor(NavigationItem.TASKS))
        assertEquals(0f, navigationContentTopPaddingFor(NavigationItem.REQUIREMENTS))
        assertEquals(0f, navigationContentTopPaddingFor(NavigationItem.ARCHIVED))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.SERVICES))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.TAG))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.SKILLS))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.SETTINGS))
    }

    @Test
    fun `repositories Skills and settings follow the task navigation group`() {
        val expected = listOf(
            NavigationItem.TASKS,
            NavigationItem.REQUIREMENTS,
            NavigationItem.TAG,
            NavigationItem.SERVICES,
            NavigationItem.SKILLS,
            NavigationItem.SETTINGS,
        )

        assertEquals(expected, visibleNavigationItemsFor(showTagNavigation = true))
        assertEquals(expected - NavigationItem.TAG, visibleNavigationItemsFor(showTagNavigation = false))
    }

    @Test
    fun `archived task route is hidden while remaining inside development tasks navigation`() {
        val items = visibleNavigationItemsFor(showTagNavigation = true)

        assertEquals(NavigationItem.TASKS, sidebarNavigationSelection(NavigationItem.ARCHIVED))
        assertEquals(NavigationItem.TASKS, items.first())
        assertEquals(NavigationItem.REQUIREMENTS, items[1])
        assertFalse(NavigationItem.ARCHIVED in items)
        assertTrue(NavigationItem.REQUIREMENTS in visibleNavigationItemsFor(showTagNavigation = false))
    }

    @Test
    fun `task service and settings pages share the eight dp content start gutter`() {
        assertEquals(8f, MAIN_CONTENT_START_PADDING_DP)
        assertEquals(MAIN_CONTENT_START_PADDING_DP, taskScreenHorizontalPadding())
        assertEquals(28f, MAIN_CONTENT_END_PADDING_DP)
        assertEquals(28f, MAIN_CONTENT_BOTTOM_PADDING_DP)
    }

    @Test
    fun `estimate badges preserve full decimals in both layouts and announce days instead of counts`() {
        listOf("0" to "0", "12.500" to "12.5", "1000.00" to "1000").forEach { (value, label) ->
            val badge = requirementsNavigationBadge(java.math.BigDecimal(value))
            assertEquals(label, badge.label)
            assertEquals(label, badge.compactLabel)
            assertEquals("所选 Sprint 我的估时总计：$label 天", badge.description)
        }
        val unknown = requirementsNavigationBadge(null)
        assertEquals("—", unknown.label)
        assertEquals("—", unknown.compactLabel)
        assertEquals("所选 Sprint 我的估时总计：暂不可用", unknown.description)
    }

    @Test
    fun `compact badges omit empty counts and preserve exact positive counts`() {
        assertNull(compactNavigationCountLabel(null))
        assertNull(compactNavigationCountLabel(0))
        assertEquals("7", compactNavigationCountLabel(7))
        assertEquals("9", compactNavigationCountLabel(9))
        assertEquals("10", compactNavigationCountLabel(10))
        assertEquals("26", compactNavigationCountLabel(26))
        assertEquals("100", compactNavigationCountLabel(100))
    }
}
