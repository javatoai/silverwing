package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AgentTaskTemplate

/** Outcome of toggling a template in the create-task form. */
sealed interface TemplateFillResult {
    /** Apply immediately: replace the notes and update the selection. */
    data class Applied(val notes: String, val selectedTemplateId: String?) : TemplateFillResult

    /** The user has hand-edited the notes; replacing them requires confirmation. */
    data class NeedsConfirmation(val target: AgentTaskTemplate) : TemplateFillResult
}

/**
 * Restores a visual template selection for an existing task without persisting
 * template metadata into the task. Equal contents are deliberately ambiguous:
 * selecting none is safer than presenting an arbitrary template as the source.
 */
fun selectedTemplateIdForNotes(notes: String, templates: List<AgentTaskTemplate>): String? =
    templates.filter { it.content == notes }.singleOrNull()?.id

/**
 * Single-selection template filling for the create-task notes field.
 *
 * A template owns the notes only while the notes still equal its content; any
 * manual edit makes the notes user-owned again, so switching or clearing the
 * selection must never silently discard them.
 */
fun resolveTemplateToggle(
    notes: String,
    selectedTemplate: AgentTaskTemplate?,
    target: AgentTaskTemplate,
): TemplateFillResult = when {
    // Toggling the selected template off clears the notes only while untouched.
    selectedTemplate?.id == target.id ->
        TemplateFillResult.Applied(if (notes == target.content) "" else notes, null)

    // No selection yet or the previous template content is still intact: safe to fill.
    notes.isBlank() || notes == selectedTemplate?.content ->
        TemplateFillResult.Applied(target.content, target.id)

    else -> TemplateFillResult.NeedsConfirmation(target)
}
