package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import kotlin.test.Test
import kotlin.test.assertEquals

class TaskToolbarPresentationTest {
    @Test
    fun `task toolbar independently exposes optional path and git action groups`() {
        listOf(
            false to false,
            false to true,
            true to false,
            true to true,
        ).forEach { (showPath, showGit) ->
            val presentation = taskDetailToolbarPresentationFor(
                AppConfig(
                    showTaskDetailPathActionGroup = showPath,
                    showTaskDetailGitActionGroup = showGit,
                ),
            )

            assertEquals(showPath, presentation.showPathActionGroup)
            assertEquals(showGit, presentation.showGitActionGroup)
        }
    }
}
