package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlin.test.*
import kotlinx.serialization.json.Json
import kotlinx.serialization.encodeToString

class ServiceEditorDraftsTest {
    @Test fun `form and json use the same validation and parse errors do not repeat raw json`() {
        val json = Json { encodeDefaults = true }
        val invalid = BootstrapConfig(commands = listOf(BootstrapCommand(name = "init", executable = "")))
        assertFailsWith<ServiceEditorValidationException> { invalid.toEditorDraft().toConfig() }
        assertEquals("json", assertFailsWith<ServiceEditorValidationException> { parseBootstrapEditorJson(json, json.encodeToString(invalid.commands), BootstrapEditorPage.COMMANDS) }.issue.field)
        val raw = "[]broken"
        val issue = assertFailsWith<ServiceEditorValidationException> { parseBootstrapEditorJson(json, raw, BootstrapEditorPage.COPIES) }.issue
        assertTrue(issue.message.startsWith("JSON 格式错误"))
        assertFalse(issue.message.contains(raw))
        assertEquals(BootstrapConfig(), parseBootstrapEditorJson(json, "[]", BootstrapEditorPage.COPIES))
        assertEquals(BootstrapConfig(), parseBootstrapEditorJson(json, "[]", BootstrapEditorPage.COMMANDS))
        assertFailsWith<ServiceEditorValidationException> { parseBootstrapEditorJson(json, "{}", BootstrapEditorPage.COPIES) }
    }

    @Test fun `json conversion keeps surviving repeated entry identities`() {
        val rule = BootstrapCopyRule("a", "b")
        val original = BootstrapConfig(listOf(rule, rule), listOf(BootstrapCommand(name = "init", executable = "tool"))).toEditorDraft()
        val changed = original.reconcile(BootstrapConfig(listOf(rule, rule.copy(target = "c")), listOf(BootstrapCommand(name = "init", executable = "other"))))
        assertEquals(original.copies.map { it.key }, changed.copies.map { it.key })
        assertEquals(original.commands.single().key, changed.commands.single().key)
        assertEquals("other", changed.toConfig().commands.single().executable)
    }

    @Test fun `empty and complete drafts use bootstrap validation`() {
        assertEquals(BootstrapConfig(), BootstrapEditorDraft().toConfig())
        val config = BootstrapConfig(listOf(BootstrapCopyRule("config/a", "config/b")), listOf(BootstrapCommand(name = "init", executable = "tool", arguments = listOf("a", "b"))))
        assertEquals(config, config.toEditorDraft().toConfig())
    }

    @Test fun `bad copy paths locate first field without losing input`() {
        for (path in listOf("", "../a", ".git/config")) {
            val entry = BootstrapCopyDraft(rule = BootstrapCopyRule(path, "target"))
            val draft = BootstrapEditorDraft(copies = listOf(entry))
            val failure = assertFailsWith<ServiceEditorValidationException> { draft.toConfig() }
            assertEquals(ServiceEditorError("copies", entry.key, "source", failure.message!!), failure.issue)
            assertEquals(path, draft.copies.single().rule.source)
        }
    }

    @Test fun `raw invalid timeouts survive deletion and conversion failures`() {
        val first = BootstrapCommandDraft(command = BootstrapCommand(name = "first", executable = "tool"))
        for (timeout in listOf("", "abc", "0", "-2", "9223372036854775808")) {
            val second = BootstrapCommandDraft(command = BootstrapCommand(name = "second", executable = "tool"), timeoutText = timeout, argumentsText = "one\n\ntwo\n")
            val draft = BootstrapEditorDraft(commands = listOf(first, second))
            val remaining = draft.copy(commands = draft.commands.filter { it.key != first.key })
            val issue = assertFailsWith<ServiceEditorValidationException> { remaining.toConfig() }.issue
            assertEquals(second.key, issue.itemId)
            assertEquals("timeout", issue.field)
            assertEquals(timeout, remaining.commands.single().timeoutText)
            assertEquals("one\n\ntwo\n", remaining.commands.single().argumentsText)
        }
    }

    @Test fun `incomplete commands and module errors have section and item identity`() {
        val entry = BootstrapCommandDraft()
        assertEquals("executable", assertFailsWith<ServiceEditorValidationException> { BootstrapEditorDraft(commands = listOf(entry)).toConfig() }.issue.field)
        val command = WorkspaceCommandEditorDraft("command", "Command", "build", "tool", emptyList(), "../outside", "10", true)
        val module = ServiceModuleEditorDraft("module", customCommands = listOf(command))
        val issue = assertFailsWith<ServiceEditorValidationException> { validateModuleDrafts(listOf(module)) }.issue
        assertEquals("modules", issue.section)
        assertEquals(command.id, issue.itemId)
        assertEquals("workingDirectory", issue.field)
        assertEquals("masterBranch", assertFailsWith<ServiceEditorValidationException> { validateModuleDrafts(listOf(module.copy(masterBranch = "invalid"))) }.issue.field)
    }

    @Test fun `bounds switch navigation without extending outside small windows`() {
        for ((w, h) in listOf(1600f to 900f, 820f to 600f, 650f to 400f)) {
            val bounds = serviceEditorBounds(w, h)
            assertTrue(bounds.width < w && bounds.maxHeight < h)
            assertEquals(minOf(720f, h - 48f), bounds.height)
            assertEquals(bounds.width < 800, bounds.compact)
        }
    }

    @Test fun `each page converts independently while the other form is incomplete`() {
        val json = Json { encodeDefaults = true }
        val incompleteCommand = BootstrapCommandDraft(timeoutText = "abc")
        val initial = BootstrapConfig(copyRules = listOf(BootstrapCopyRule("a", "b"))).toEditorSession(json)
        val session = initial.copy(draft = initial.draft.copy(commands = listOf(incompleteCommand)))
        val roundTrip = session.switchMode(json, BootstrapEditorPage.COPIES, "json").switchMode(json, BootstrapEditorPage.COPIES, "form")
        assertEquals(session.draft.copies, roundTrip.draft.copies)
        assertEquals(incompleteCommand, roundTrip.draft.commands.single())
        val failure = assertFailsWith<ServiceEditorValidationException> { roundTrip.toConfig(json) }.issue
        assertEquals("commands", failure.section)
        assertEquals(incompleteCommand.key, failure.itemId)

        val validCommand = BootstrapCommandDraft(command = BootstrapCommand(name = "init", executable = "tool"))
        val otherIncomplete = initial.copy(draft = BootstrapEditorDraft(copies = listOf(BootstrapCopyDraft()), commands = listOf(validCommand)))
        assertEquals("json", otherIncomplete.switchMode(json, BootstrapEditorPage.COMMANDS, "json").commands.mode)
    }

    @Test fun `two json originals and modes survive independent conversion and save failure`() {
        val json = Json { encodeDefaults = true }
        val invalid = "[ invalid raw input"
        val session = BootstrapConfig().toEditorSession(json).copy(copies = BootstrapJsonEditor("json", invalid))
        val changed = session.switchMode(json, BootstrapEditorPage.COMMANDS, "json")
        assertEquals(session.copies, changed.copies)
        assertEquals("[]", changed.commands.text)
        val issue = assertFailsWith<ServiceEditorValidationException> { changed.toConfig(json) }.issue
        assertEquals("copies", issue.section)
        assertEquals("json", issue.field)
        assertEquals(invalid, changed.copies.text)
        assertFailsWith<ServiceEditorValidationException> { changed.switchMode(json, BootstrapEditorPage.COPIES, "form") }
        assertEquals("json", changed.copies.mode)
        assertEquals(session.copies, changed.switchMode(json, BootstrapEditorPage.COMMANDS, "form").copies)
    }

    @Test fun `save merges json and form pages without discarding either`() {
        val json = Json { encodeDefaults = true }
        val rule = BootstrapCopyRule("source", "target")
        val command = BootstrapCommand(name = "init", executable = "tool", arguments = listOf("one"))
        val session = BootstrapConfig(commands = listOf(command)).toEditorSession(json)
            .copy(copies = BootstrapJsonEditor("json", json.encodeToString(listOf(rule))))
        assertEquals(BootstrapConfig(listOf(rule), listOf(command)), session.toConfig(json))
        val bothJson = session.switchMode(json, BootstrapEditorPage.COMMANDS, "json")
        assertEquals(session.toConfig(json), bothJson.toConfig(json))
        assertEquals(BootstrapConfig(), bothJson.copy(copies = BootstrapJsonEditor("json", "[]"), commands = BootstrapJsonEditor("json", "[]")).toConfig(json))
        val badCommand = bothJson.copy(commands = BootstrapJsonEditor("json", "[{}]"))
        assertEquals("commands", assertFailsWith<ServiceEditorValidationException> { badCommand.toConfig(json) }.issue.section)
    }
}
