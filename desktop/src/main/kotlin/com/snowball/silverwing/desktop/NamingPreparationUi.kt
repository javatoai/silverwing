package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Pause
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

@Composable internal fun NamingPreparationControls(coordinator: RequirementAiNamingCoordinator, modifier: Modifier = Modifier) {
    val state by coordinator.state.collectAsState()
    if (state.total == 0) return
    var failuresOpen by remember { mutableStateOf(false) }
    val preparing = state.completed < state.total && (state.running || state.paused)
    if (preparing || state.failures > 0) {
        FlowRow(modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            if (preparing) {
                Text(state.label, Modifier.align(Alignment.CenterVertically), style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                ActionIconButton(if (state.paused) "继续命名预热" else "暂停命名预热",
                    { if (state.paused) coordinator.resume() else coordinator.pause() }, Modifier.size(28.dp)) {
                    Icon(if (state.paused) Icons.Outlined.PlayArrow else Icons.Outlined.Pause, null, Modifier.size(16.dp))
                }
            }
            if (state.failures > 0) TextButton(onClick = { failuresOpen = true }, contentPadding = PaddingValues(horizontal = 6.dp, vertical = 0.dp)) { Text("查看失败", style = MaterialTheme.typography.labelSmall) }
        }
    }
    if (failuresOpen) AlertDialog(onDismissRequest = { failuresOpen = false }, title = { Text("命名预热失败 (${state.failures})") },
        text = {
            LazyColumn(Modifier.widthIn(max = 520.dp).heightIn(max = 420.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                items(state.failedItems, key = { it.key }) { item ->
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text(item.title, style = MaterialTheme.typography.bodyMedium)
                            Text(item.message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                        }
                        TextButton(onClick = { coordinator.retry(item.key) }, enabled = item.key !in state.retrying) {
                            Text(if (item.key in state.retrying) "重试中…" else "重试")
                        }
                    }
                }
                if (state.failedItems.isEmpty()) item { Text("失败项已全部完成") }
            }
        }, confirmButton = { TextButton(onClick = coordinator::retryFailed, enabled = state.failures > 0 && state.retrying.isEmpty()) { Text("重试全部失败项") } },
        dismissButton = { TextButton(onClick = { failuresOpen = false }) { Text("关闭") } })
}
