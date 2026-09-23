package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.Text
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.PointerIcon
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import java.awt.Cursor

internal const val LOCAL_SKILLS_PANE_HANDLE_WIDTH_DP = 8f
internal const val MIN_LOCAL_SKILLS_LIST_WIDTH_DP = 200f
internal const val MIN_LOCAL_SKILLS_DIRECTORY_WIDTH_DP = 240f
internal const val MIN_LOCAL_SKILLS_PREVIEW_WIDTH_DP = 360f
internal const val MIN_LOCAL_SKILLS_PANE_CANVAS_WIDTH_DP =
    MIN_LOCAL_SKILLS_LIST_WIDTH_DP +
        MIN_LOCAL_SKILLS_DIRECTORY_WIDTH_DP +
        MIN_LOCAL_SKILLS_PREVIEW_WIDTH_DP +
        LOCAL_SKILLS_PANE_HANDLE_WIDTH_DP * 2

private const val DEFAULT_LOCAL_SKILLS_LIST_RATIO = 0.22f
private const val DEFAULT_LOCAL_SKILLS_DIRECTORY_RATIO = 0.25f

internal data class LocalSkillsPaneLayout(
    val canvasWidthDp: Float,
    val skillListWidthDp: Float,
    val directoryWidthDp: Float,
    val previewWidthDp: Float,
) {
    fun toPreferences() = LocalSkillsPaneWidthPreferences(
        skillListWidthDp = skillListWidthDp,
        directoryWidthDp = directoryWidthDp,
    )
}

internal enum class LocalSkillsPaneBoundary {
    SKILL_LIST_AND_DIRECTORY,
    DIRECTORY_AND_PREVIEW,
}

/**
 * Resolves the current canvas without allowing any pane to become unreadable. A narrow
 * viewport scrolls this minimum canvas instead of compressing the three-pane browser.
 */
internal fun resolveLocalSkillsPaneLayout(
    availableWidthDp: Float,
    preferences: LocalSkillsPaneWidthPreferences = LocalSkillsPaneWidthPreferences(),
): LocalSkillsPaneLayout {
    val canvasWidthDp = availableWidthDp.coerceAtLeast(MIN_LOCAL_SKILLS_PANE_CANVAS_WIDTH_DP)
    val contentWidthDp = canvasWidthDp - LOCAL_SKILLS_PANE_HANDLE_WIDTH_DP * 2
    val defaultSkillListWidthDp = contentWidthDp * DEFAULT_LOCAL_SKILLS_LIST_RATIO
    val defaultDirectoryWidthDp = contentWidthDp * DEFAULT_LOCAL_SKILLS_DIRECTORY_RATIO
    val maximumSkillListWidthDp = contentWidthDp - MIN_LOCAL_SKILLS_DIRECTORY_WIDTH_DP - MIN_LOCAL_SKILLS_PREVIEW_WIDTH_DP
    val skillListWidthDp = (preferences.skillListWidthDp ?: defaultSkillListWidthDp)
        .coerceIn(MIN_LOCAL_SKILLS_LIST_WIDTH_DP, maximumSkillListWidthDp)
    val maximumDirectoryWidthDp = contentWidthDp - skillListWidthDp - MIN_LOCAL_SKILLS_PREVIEW_WIDTH_DP
    val directoryWidthDp = (preferences.directoryWidthDp ?: defaultDirectoryWidthDp)
        .coerceIn(MIN_LOCAL_SKILLS_DIRECTORY_WIDTH_DP, maximumDirectoryWidthDp)
    return LocalSkillsPaneLayout(
        canvasWidthDp = canvasWidthDp,
        skillListWidthDp = skillListWidthDp,
        directoryWidthDp = directoryWidthDp,
        previewWidthDp = contentWidthDp - skillListWidthDp - directoryWidthDp,
    )
}

/** The first divider keeps the directory stable; the second keeps the Skill list stable. */
internal fun resizeLocalSkillsPaneLayout(
    availableWidthDp: Float,
    current: LocalSkillsPaneLayout,
    boundary: LocalSkillsPaneBoundary,
    dragAmountDp: Float,
): LocalSkillsPaneLayout = when (boundary) {
    LocalSkillsPaneBoundary.SKILL_LIST_AND_DIRECTORY -> resolveLocalSkillsPaneLayout(
        availableWidthDp = availableWidthDp,
        preferences = LocalSkillsPaneWidthPreferences(
            skillListWidthDp = current.skillListWidthDp + dragAmountDp,
            directoryWidthDp = current.directoryWidthDp,
        ),
    )

    LocalSkillsPaneBoundary.DIRECTORY_AND_PREVIEW -> resolveLocalSkillsPaneLayout(
        availableWidthDp = availableWidthDp,
        preferences = LocalSkillsPaneWidthPreferences(
            skillListWidthDp = current.skillListWidthDp,
            directoryWidthDp = current.directoryWidthDp + dragAmountDp,
        ),
    )
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
internal fun LocalSkillsPaneResizeHandle(
    label: String,
    onResizeStart: () -> Unit,
    onResize: (Float) -> Unit,
) {
    val currentOnResizeStart by rememberUpdatedState(onResizeStart)
    val currentOnResize by rememberUpdatedState(onResize)
    val density = LocalDensity.current
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text(label) } },
        state = rememberTooltipState(),
    ) {
        Box(
            Modifier
                .width(LOCAL_SKILLS_PANE_HANDLE_WIDTH_DP.dp)
                .fillMaxHeight()
                .pointerHoverIcon(PointerIcon(Cursor.getPredefinedCursor(Cursor.E_RESIZE_CURSOR)))
                .pointerInput(density) {
                    detectHorizontalDragGestures(
                        onDragStart = { currentOnResizeStart() },
                        onHorizontalDrag = { change, dragAmount ->
                            change.consume()
                            currentOnResize(with(density) { dragAmount.toDp().value })
                        },
                    )
                },
            contentAlignment = Alignment.Center,
        ) {
            Box(
                Modifier
                    .fillMaxHeight()
                    .padding(vertical = 16.dp)
                    .width(2.dp)
                    .background(androidx.compose.material3.MaterialTheme.colorScheme.outlineVariant),
            )
        }
    }
}
