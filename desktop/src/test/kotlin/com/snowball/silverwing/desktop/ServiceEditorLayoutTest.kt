package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.TagBuildMode
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertSame
import kotlin.test.assertTrue
import androidx.compose.ui.Modifier
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.unit.dp

class ServiceEditorLayoutTest {
    @Test
    fun `module editor keeps inherited master separate from explicit override`() {
        val inherited = ServiceModuleEditorDraft(id = "default")
        assertNull(inherited.toConfig().masterBranch)

        val custom = inherited.copy(masterBranch = "upstream/main")
        assertEquals("upstream/main", custom.toConfig().masterBranch)
        assertEquals("upstream/main", custom.toConfig().toEditorDraft().masterBranch)
        assertNull(custom.copy(masterBranch = null).toConfig().masterBranch)
    }

    @Test
    fun `module editor keeps inherited test target distinct from explicit override`() {
        val inherited = ServiceModuleEditorDraft(id = "default")
        assertNull(inherited.toConfig().tagTargetRef)

        val override = inherited.copy(tagTargetRef = "origin/qa")
        assertEquals("origin/qa", override.toConfig().tagTargetRef)
        assertEquals("origin/qa", override.toConfig().toEditorDraft().tagTargetRef)
        assertNull(override.copy(tagMode = TagBuildMode.CURRENT_BRANCH).toConfig().tagTargetRef)
    }

    @Test
    fun `service editor widens to host the section navigation`() {
        val wide = serviceEditorBounds(1500f, 900f)
        assertEquals(1000f, wide.width)
        assertTrue(!wide.compact)
        assertTrue(serviceEditorBounds(839f, 600f).compact)
        assertTrue(!serviceEditorBounds(840f, 600f).compact)
    }

    @Test
    fun `tag target and message fields use intrinsic single line height`() {
        val layout = tagConfigurationFieldLayout()

        assertNull(layout.heightDp)
        assertTrue(layout.singleLine)
    }

    @Test
    fun `tag target and message fields split the row at its center`() {
        val layout = tagConfigurationFieldLayout()

        assertEquals(layout.messageWeight, layout.targetWeight)
    }

    @Test
    fun `path field puts the supplied modifier on the text field`() {
        val supplied = Modifier.padding(1.dp)

        val targets = pathFieldModifierTargets(supplied)

        assertSame(Modifier, targets.row)
        assertSame(supplied, targets.textField)
    }
}
