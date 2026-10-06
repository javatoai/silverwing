package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals

class TaskListSplitPaneTest {
    @Test
    fun `narrow content uses one focused master detail pane instead of three columns`() {
        assertEquals(
            TaskMasterDetailLayout.FOCUSED_PANE,
            taskMasterDetailLayout(TASK_MASTER_DETAIL_FOCUSED_MAX_WIDTH_DP - 1f),
        )
    }

    @Test
    fun `wide content keeps the resizable split pane`() {
        assertEquals(
            TaskMasterDetailLayout.SPLIT_PANE,
            taskMasterDetailLayout(TASK_MASTER_DETAIL_FOCUSED_MAX_WIDTH_DP),
        )
    }

    @Test
    fun `task list uses the compact default width when there is enough room`() {
        assertEquals(240f, resolveTaskListPaneWidth(preferredWidthDp = null, availableWidthDp = 1_200f))
    }

    @Test
    fun `task list width never becomes narrower than the minimum`() {
        assertEquals(160f, resolveTaskListPaneWidth(preferredWidthDp = 120f, availableWidthDp = 1_200f))
        assertEquals(180f, resolveTaskListPaneWidth(preferredWidthDp = 180f, availableWidthDp = 1_200f))
    }

    @Test
    fun `saved task list width is used when the window can accommodate it`() {
        assertEquals(420f, resolveTaskListPaneWidth(preferredWidthDp = 420f, availableWidthDp = 1_200f))
    }

    @Test
    fun `narrow windows clamp the task list and reserve detail space`() {
        assertEquals(392f, resolveTaskListPaneWidth(preferredWidthDp = 500f, availableWidthDp = 1_000f))
        assertEquals(
            MIN_TASK_DETAIL_PANE_WIDTH_DP,
            1_000f - 392f - TASK_LIST_PANE_HANDLE_WIDTH_DP,
        )
    }

}
