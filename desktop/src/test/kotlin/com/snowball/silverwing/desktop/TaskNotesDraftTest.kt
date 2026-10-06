package com.snowball.silverwing.desktop

import kotlin.test.*

class TaskNotesDraftTest {
    @Test fun `late async read and unrelated refresh cannot overwrite newly entered draft`() {
        val draft = TaskNotesDraft()
        val initial = draft.beginRead()
        draft.edit("用户新输入\n\n")
        draft.completeRead(initial, "older disk notes")
        assertEquals("用户新输入\n\n", draft.notes)
        assertTrue(draft.ready)
        assertTrue(draft.dirty)
        val refreshed = draft.beginRead()
        draft.completeRead(refreshed, "用户新输入")
        assertEquals("用户新输入\n\n", draft.notes)
    }

    @Test fun `replacement request and explicit disk resolution invalidate older reads`() {
        val draft = TaskNotesDraft()
        val older = draft.beginRead()
        val newer = draft.beginRead()
        draft.completeRead(older, "old")
        assertTrue(draft.loading)
        draft.completeRead(newer, "new")
        assertEquals("new", draft.notes)
        draft.edit("local")
        val pending = draft.beginRead()
        draft.acceptDisk("chosen disk", force = true)
        draft.completeRead(pending, "local")
        assertEquals("chosen disk", draft.notes)
        assertFalse(draft.dirty)
    }

    @Test fun `failed refresh retains usable draft and save acknowledgement cannot clear later edit`() {
        val draft = TaskNotesDraft()
        draft.completeRead(draft.beginRead(), "initial")
        draft.failRead(draft.beginRead(), IllegalStateException("offline"))
        assertTrue(draft.ready)
        assertEquals("initial", draft.notes)
        assertEquals("offline", draft.error)
        draft.edit("first edit")
        val savingRevision = draft.contentRevision
        draft.edit("second edit")
        draft.markSaved(savingRevision)
        assertTrue(draft.dirty)
        assertEquals("second edit", draft.notes)
        draft.markSaved(draft.contentRevision)
        assertFalse(draft.dirty)
    }
}
