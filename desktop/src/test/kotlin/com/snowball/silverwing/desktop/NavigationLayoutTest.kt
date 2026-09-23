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
        assertEquals(184f, sidebarWidthFor(NavigationLayout.EXPANDED))
        assertEquals(72f, sidebarWidthFor(NavigationLayout.COMPACT))
    }

    @Test
    fun `all page titles are removed while non task destinations retain a compact top gutter`() {
        assertEquals(0f, navigationContentTopPaddingFor(NavigationItem.TASKS))
        assertEquals(0f, navigationContentTopPaddingFor(NavigationItem.REQUIREMENTS))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.ARCHIVED))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.SERVICES))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.TAG))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.SKILLS))
        assertEquals(16f, navigationContentTopPaddingFor(NavigationItem.SETTINGS))
    }

    @Test
    fun `Skills remains visible between Tag builds and settings`() {
        val withTag = visibleNavigationItemsFor(showTagNavigation = true)
        val skillsIndex = withTag.indexOf(NavigationItem.SKILLS)

        assertEquals(NavigationItem.TAG, withTag[skillsIndex - 1])
        assertEquals(NavigationItem.SETTINGS, withTag[skillsIndex + 1])
        assertTrue(NavigationItem.SKILLS in visibleNavigationItemsFor(showTagNavigation = false))
        assertFalse(NavigationItem.TAG in visibleNavigationItemsFor(showTagNavigation = false))
    }

    @Test
    fun `requirement list sits immediately below development tasks`() {
        val items = visibleNavigationItemsFor(showTagNavigation = true)
        val index = items.indexOf(NavigationItem.REQUIREMENTS)

        assertEquals(NavigationItem.TASKS, items[index - 1])
        assertEquals(NavigationItem.ARCHIVED, items[index + 1])
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
    fun `compact badges omit empty counts and cap long counts`() {
        assertNull(compactNavigationCountLabel(null))
        assertNull(compactNavigationCountLabel(0))
        assertEquals("7", compactNavigationCountLabel(7))
        assertEquals("9+", compactNavigationCountLabel(10))
    }
}
