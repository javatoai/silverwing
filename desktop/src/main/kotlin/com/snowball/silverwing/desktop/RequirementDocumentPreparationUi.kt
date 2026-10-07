package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

@Composable
internal fun RequirementDocumentPreparationControls(coordinator: RequirementDocumentPreparation) {
    val state by coordinator.state.collectAsState()
    RequirementDocumentPreparationStatus(state, coordinator::retryFailed)
}

@Composable
internal fun RequirementDocumentPreparationStatus(state: RequirementDocumentPreparationState, onRetry: () -> Unit) {
    if (state.total == 0) return
    FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text("全文缓存 ${state.completed}/${state.total}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (state.failures > 0) {
            Text("${state.failures} 项失败", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            TextButton(onClick = onRetry, enabled = !state.running, contentPadding = PaddingValues(horizontal = 6.dp, vertical = 0.dp),
                modifier = Modifier.heightIn(min = 24.dp)) { Text("重试失败项", style = MaterialTheme.typography.labelSmall) }
        }
    }
}
