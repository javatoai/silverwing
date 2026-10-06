package com.snowball.silverwing.desktop

import androidx.compose.foundation.clickable
import androidx.compose.foundation.hoverable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.LocalMinimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.gitRemoteBrowserUrl
import com.snowball.silverwing.core.RepositoryRemoteAddressFailure

@Composable
internal fun ServiceRemoteAddresses(state: RepositoryAddressesState, onRetry: () -> Unit, onOpen: (String) -> Unit, onCopy: (String) -> Unit, enabled: Boolean = true,
    failure: RepositoryRemoteAddressFailure? = null) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        when (state) {
            RepositoryAddressesState.Loading -> Text("正在读取远程地址…", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            RepositoryAddressesState.Failed -> Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(failure?.message ?: "远程地址读取失败", Modifier.weight(1f), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                TextButton(onClick = onRetry, enabled = enabled) { Text("重试") }
            }
            is RepositoryAddressesState.Loaded -> {
                if (state.addresses.isEmpty()) Text("未配置远程地址", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                state.addresses.forEach { address ->
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(9.dp)) {
                        val purpose = when { address.fetch && address.push -> ""; address.fetch -> " · 拉取"; else -> " · 推送" }
                        Text(address.remote + purpose, Modifier.widthIn(max = 120.dp).padding(top = 4.dp), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        val url = gitRemoteBrowserUrl(address.gitUrl)
                        val interaction = remember(address) { MutableInteractionSource() }
                        val hovered by interaction.collectIsHoveredAsState()
                        val focused by interaction.collectIsFocusedAsState()
                        SelectionContainer(Modifier.weight(1f)) {
                            Text(address.gitUrl,
                                Modifier.fillMaxWidth().padding(vertical = 4.dp).then(if (url == null) Modifier else
                                    Modifier.hoverable(interaction).clickable(enabled = enabled, interactionSource = interaction, indication = null, role = Role.Button, onClickLabel = "打开远程仓库") { onOpen(url) }),
                                style = MaterialTheme.typography.bodySmall,
                                color = if (url == null || !enabled) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.primary,
                                textDecoration = if (url != null && enabled && (hovered || focused)) TextDecoration.Underline else null,
                            )
                        }
                        CompositionLocalProvider(LocalMinimumInteractiveComponentSize provides 28.dp) {
                            ActionIconButton("复制远程地址", { onCopy(address.gitUrl) }, Modifier.size(28.dp), enabled = enabled) { Icon(Icons.Outlined.ContentCopy, "复制远程地址", Modifier.size(15.dp)) }
                        }
                    }
                }
            }
        }
    }
}
