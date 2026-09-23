package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals

class LocalSkillsSplitPaneTest {
    @Test
    fun `default layout prioritizes preview`() {
        val layout = resolveLocalSkillsPaneLayout(availableWidthDp = 1_200f)

        assertEquals(1_200f, layout.canvasWidthDp)
        assertEquals(260.48f, layout.skillListWidthDp, absoluteTolerance = 0.01f)
        assertEquals(296f, layout.directoryWidthDp, absoluteTolerance = 0.01f)
        assertEquals(627.52f, layout.previewWidthDp, absoluteTolerance = 0.01f)
    }

    @Test
    fun `left handle keeps directory width fixed and lets preview absorb the change`() {
        val current = resolveLocalSkillsPaneLayout(availableWidthDp = 1_200f)
        val resized = resizeLocalSkillsPaneLayout(
            availableWidthDp = 1_200f,
            current = current,
            boundary = LocalSkillsPaneBoundary.SKILL_LIST_AND_DIRECTORY,
            dragAmountDp = 40f,
        )

        assertEquals(300.48f, resized.skillListWidthDp, absoluteTolerance = 0.01f)
        assertEquals(current.directoryWidthDp, resized.directoryWidthDp, absoluteTolerance = 0.01f)
        assertEquals(587.52f, resized.previewWidthDp, absoluteTolerance = 0.01f)
    }

    @Test
    fun `right handle preserves the preview minimum width`() {
        val current = resolveLocalSkillsPaneLayout(availableWidthDp = 1_200f)
        val resized = resizeLocalSkillsPaneLayout(
            availableWidthDp = 1_200f,
            current = current,
            boundary = LocalSkillsPaneBoundary.DIRECTORY_AND_PREVIEW,
            dragAmountDp = 500f,
        )

        assertEquals(MIN_LOCAL_SKILLS_PREVIEW_WIDTH_DP, resized.previewWidthDp)
        assertEquals(563.52f, resized.directoryWidthDp, absoluteTolerance = 0.01f)
    }

    @Test
    fun `narrow viewport keeps a readable scrollable minimum canvas`() {
        val layout = resolveLocalSkillsPaneLayout(availableWidthDp = 640f)

        assertEquals(MIN_LOCAL_SKILLS_PANE_CANVAS_WIDTH_DP, layout.canvasWidthDp)
        assertEquals(MIN_LOCAL_SKILLS_LIST_WIDTH_DP, layout.skillListWidthDp)
        assertEquals(MIN_LOCAL_SKILLS_DIRECTORY_WIDTH_DP, layout.directoryWidthDp)
        assertEquals(MIN_LOCAL_SKILLS_PREVIEW_WIDTH_DP, layout.previewWidthDp)
    }
}
