package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RemoteNamePickerPresentationTest {
    @Test
    fun `hides source picker when the selected source is the only remote`() {
        assertFalse(
            shouldShowRemoteNamePicker(
                selectedRemote = "origin",
                state = RepositoryRemotesState.Loaded(listOf("ORIGIN")),
            ),
        )
    }

    @Test
    fun `keeps source picker when more than one source is available`() {
        assertTrue(
            shouldShowRemoteNamePicker(
                selectedRemote = "origin",
                state = RepositoryRemotesState.Loaded(listOf("origin", "upstream")),
            ),
        )
    }

    @Test
    fun `does not render source picker while its remote list is loading`() {
        assertFalse(shouldShowRemoteNamePicker("origin", RepositoryRemotesState.Idle))
        assertFalse(shouldShowRemoteNamePicker("origin", RepositoryRemotesState.Loading))
    }

    @Test
    fun `keeps source picker after failure or when configured source is gone`() {
        assertTrue(shouldShowRemoteNamePicker("origin", RepositoryRemotesState.Failed("offline")))
        assertTrue(
            shouldShowRemoteNamePicker(
                selectedRemote = "upstream",
                state = RepositoryRemotesState.Loaded(listOf("origin")),
            ),
        )
    }
}
