package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.RemoteBranchRef
import com.snowball.silverwing.core.RemoteBranchSearch
import java.nio.charset.StandardCharsets
import java.util.Base64
import java.util.prefs.Preferences

/** Shared editable branch chooser. All Git I/O remains in [DesktopApplication]. */
@Composable
internal fun RemoteBranchPicker(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    repositoryId: String,
    controller: DesktopApplication,
    modifier: Modifier = Modifier,
    remote: String? = null,
) {
    var expanded by remember { mutableStateOf(false) }
    var query by remember { mutableStateOf("") }
    var highlightedIndex by remember { mutableStateOf(0) }
    val searchFocusRequester = remember { FocusRequester() }
    val effectiveRemote = remote?.takeIf(String::isNotBlank)
        ?: runCatching { RemoteBranchRef.parse(value.trim()).remote }.getOrDefault("origin")
    val state = controller.remoteBranchState(repositoryId, effectiveRemote)
    val availableBranches = remoteBranchOptions(state)
    val recentBranches = remember(repositoryId, effectiveRemote, expanded) {
        RecentBranchHistory.list(repositoryId, effectiveRemote)
    }
    val matchingBranches = RemoteBranchSearch.filter(
        mergeRecentBranches(recentBranches, availableBranches),
        query,
    )
    LaunchedEffect(expanded) {
        if (expanded) {
            highlightedIndex = 0
            searchFocusRequester.requestFocus()
        }
    }
    LaunchedEffect(query, availableBranches) {
        highlightedIndex = 0
    }
    val closeMenu = {
        expanded = false
        controller.cancelRemoteBranchLoads()
    }
    val chooseBranch: (String) -> Unit = { branch ->
        RecentBranchHistory.record(repositoryId, effectiveRemote, branch)
        onValueChange(branch)
        closeMenu()
    }
    val openMenu = {
        controller.loadRemoteBranches(repositoryId, effectiveRemote)
        query = ""
        expanded = true
    }
    Box(modifier) {
        OutlinedTextField(
            value,
            onValueChange,
            Modifier.fillMaxWidth().onFocusChanged { focus -> if (focus.isFocused && !expanded) openMenu() },
            label = { Text(label) },
            singleLine = true,
            colors = branchPickerFieldColors(),
            trailingIcon = {
                ActionIconButton("搜索并选择远程分支", openMenu) {
                    Icon(Icons.Outlined.KeyboardArrowDown, "选择远程分支")
                }
            },
        )
        SilverWingBranchPopup(
            expanded = expanded,
            onDismissRequest = closeMenu,
        ) {
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                OutlinedTextField(
                    query,
                    { query = it },
                    Modifier.weight(1f)
                        .focusRequester(searchFocusRequester)
                        .onPreviewKeyEvent { event ->
                            if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                            when (event.key) {
                                Key.DirectionDown -> {
                                    if (matchingBranches.isNotEmpty()) highlightedIndex = (highlightedIndex + 1).coerceAtMost(matchingBranches.lastIndex)
                                    true
                                }
                                Key.DirectionUp -> {
                                    if (matchingBranches.isNotEmpty()) highlightedIndex = (highlightedIndex - 1).coerceAtLeast(0)
                                    true
                                }
                                Key.Enter -> {
                                    matchingBranches.getOrNull(highlightedIndex)?.let(chooseBranch)
                                    matchingBranches.isNotEmpty()
                                }
                                Key.Escape -> {
                                    closeMenu()
                                    true
                                }
                                else -> false
                            }
                        },
                    label = { Text("搜索远程分支") },
                    singleLine = true,
                    colors = branchPickerFieldColors(),
                )
                ActionIconButton(
                    "刷新远程分支",
                    { controller.loadRemoteBranches(repositoryId, effectiveRemote, true) },
                    Modifier.size(36.dp),
                    enabled = state !is RemoteBranchesState.Loading,
                ) { Icon(Icons.Outlined.Refresh, "刷新远程分支", Modifier.size(18.dp)) }
            }
            when (state) {
                is RemoteBranchesState.Loading -> DropdownMenuItem(
                    text = { Text(if (state.staleBranches.isEmpty()) "正在读取远程分支…" else "正在刷新，以下为上次结果") },
                    onClick = {},
                    enabled = false,
                )
                is RemoteBranchesState.Failed -> DropdownMenuItem(
                    text = {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Text(
                                if (state.staleBranches.isEmpty()) "加载失败：${state.message}" else "刷新失败，以下为上次结果",
                                Modifier.weight(1f),
                                color = MaterialTheme.colorScheme.error,
                                maxLines = 2,
                                overflow = TextOverflow.Ellipsis,
                            )
                            ActionIconButton("复制远程分支错误", {
                                controller.copyText(state.message, "错误详情已复制")
                            }, Modifier.size(30.dp)) {
                                Icon(Icons.Outlined.ContentCopy, "复制错误", Modifier.size(15.dp))
                            }
                        }
                    },
                    onClick = {},
                )
                is RemoteBranchesState.Loaded -> Unit
                RemoteBranchesState.Idle -> DropdownMenuItem(text = { Text("正在准备读取远程分支…") }, onClick = {}, enabled = false)
            }
            if (state !is RemoteBranchesState.Idle) {
                if (matchingBranches.isEmpty() && state !is RemoteBranchesState.Loading) {
                    DropdownMenuItem(text = { Text("没有匹配分支") }, onClick = {}, enabled = false)
                }
                LazyColumn(Modifier.heightIn(max = 320.dp)) {
                    itemsIndexed(matchingBranches, key = { _, branch -> branch }) { index, branch ->
                        DropdownMenuItem(
                            text = {
                                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                    Text(branch, Modifier.weight(1f))
                                    if (branch in recentBranches) Text("最近", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.primary)
                                }
                            },
                            onClick = { chooseBranch(branch) },
                            modifier = if (index == highlightedIndex) Modifier.background(MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.55f)) else Modifier,
                        )
                    }
                }
            }
        }
    }
}

internal fun mergeRecentBranches(recent: List<String>, available: List<String>): List<String> =
    (recent.filter { it in available } + available).distinct()

internal object RecentBranchHistory {
    private const val MAX_ENTRIES = 5
    private const val SEPARATOR = '\u001F'
    private val preferences = Preferences.userRoot().node("com/snowball/silverwing/recent-branches")

    fun list(repositoryId: String, remote: String): List<String> = preferences
        .get(key(repositoryId, remote), "")
        .split(SEPARATOR)
        .filter(String::isNotBlank)
        .take(MAX_ENTRIES)

    fun record(repositoryId: String, remote: String, branch: String) {
        val normalized = branch.trim()
        if (normalized.isEmpty()) return
        preferences.put(
            key(repositoryId, remote),
            (listOf(normalized) + list(repositoryId, remote)).distinct().take(MAX_ENTRIES).joinToString(SEPARATOR.toString()),
        )
    }

    private fun key(repositoryId: String, remote: String): String = Base64.getUrlEncoder().withoutPadding()
        .encodeToString("$repositoryId|$remote".toByteArray(StandardCharsets.UTF_8))
}

internal fun remoteBranchOptions(state: RemoteBranchesState): List<String> = when (state) {
    is RemoteBranchesState.Loaded -> state.branches
    is RemoteBranchesState.Loading -> state.staleBranches
    is RemoteBranchesState.Failed -> state.staleBranches
    RemoteBranchesState.Idle -> emptyList()
}

@Composable
internal fun branchPickerFieldColors() = OutlinedTextFieldDefaults.colors(
    focusedContainerColor = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.72f),
    unfocusedContainerColor = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.62f),
    disabledContainerColor = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.45f),
)

@Composable
internal fun RemoteNamePicker(
    value: String,
    repositoryId: String,
    controller: DesktopApplication,
    onSelected: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    var expanded by remember(repositoryId) { mutableStateOf(false) }
    val state = controller.repositoryRemotesState(repositoryId)
    LaunchedEffect(repositoryId) {
        controller.loadRepositoryRemotes(repositoryId)
    }
    if (!shouldShowRemoteNamePicker(value, state)) return
    Box(modifier) {
        OutlinedButton(
            onClick = {
                expanded = true
                controller.loadRepositoryRemotes(repositoryId)
            },
            modifier = Modifier.fillMaxWidth().height(56.dp),
        ) {
            Text(value, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
            Icon(Icons.Outlined.KeyboardArrowDown, null, Modifier.size(17.dp))
        }
        SilverWingDropdownMenu(expanded, onDismissRequest = { expanded = false }) {
            when (state) {
                RepositoryRemotesState.Idle, RepositoryRemotesState.Loading ->
                    DropdownMenuItem({ Text("正在读取远程…") }, onClick = {}, enabled = false)
                is RepositoryRemotesState.Failed -> {
                    DropdownMenuItem({ Text(state.message, color = MaterialTheme.colorScheme.error) }, onClick = {}, enabled = false)
                    DropdownMenuItem({ Text("重试") }, onClick = { controller.loadRepositoryRemotes(repositoryId, force = true) })
                }
                is RepositoryRemotesState.Loaded -> state.remotes.forEach { remote ->
                    DropdownMenuItem(
                        text = { Text(remote) },
                        onClick = { onSelected(remote); expanded = false },
                    )
                }
            }
        }
    }
}

/**
 * A sole, still-valid source remote has no user choice to expose. Keep the picker visible
 * while loading or after an error, and when an old configuration refers to a removed remote,
 * so we never silently replace the selected source.
 */
internal fun shouldShowRemoteNamePicker(
    selectedRemote: String,
    state: RepositoryRemotesState,
): Boolean = when (state) {
    is RepositoryRemotesState.Loaded ->
        state.remotes.singleOrNull()?.equals(selectedRemote, ignoreCase = true) != true
    is RepositoryRemotesState.Failed -> true
    RepositoryRemotesState.Idle, RepositoryRemotesState.Loading -> false
}
