package com.snowball.silverwing.desktop

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
    fun `service editor widens to host the section navigation`() {
        val policy = serviceEditorDialogWidthPolicy()

        assertEquals(0.72f, policy.fillFraction)
        assertEquals(860, policy.minWidthDp)
        assertEquals(1200, policy.maxWidthDp)
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
