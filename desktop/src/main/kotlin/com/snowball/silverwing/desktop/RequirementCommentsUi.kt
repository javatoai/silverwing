package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.RequirementComments
import java.net.URI
import java.nio.file.Path

@Composable
internal fun RequirementCommentsContent(
    reader: RequirementCommentsController,
    item: ParticipatedWorkItem,
    modifier: Modifier = Modifier,
    onCopySource: (String) -> Unit,
    onCopyPath: (Path) -> Unit,
    onCopyFile: (Path) -> Unit,
) {
    val document = reader.document
    Column(modifier) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Spacer(Modifier.weight(1f))
            ActionIconButton("刷新评论", { reader.select(item, force = true) }, Modifier.size(32.dp), enabled = !reader.loading) {
                Icon(Icons.Outlined.Refresh, "刷新评论", Modifier.size(18.dp))
            }
        }
        if (reader.loading) LinearProgressIndicator(Modifier.fillMaxWidth())
        reader.error?.let { error ->
            Row(Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(error, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                TextButton(onClick = { reader.select(item, force = true) }, enabled = !reader.loading) { Text("重试评论") }
            }
        }
        document?.data?.warnings?.forEach { warning ->
            Text(warning, Modifier.padding(horizontal = 14.dp, vertical = 4.dp),
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (document == null || document.data.comments.isEmpty()) {
            Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
                if (reader.loading && document == null) CircularProgressIndicator()
                else if (document != null) Text("暂无评论", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else {
            val content = remember(document.data) { requirementCommentsMarkdown(document.data) }
            CompositionLocalProvider(LocalMarkdownRemoteImages provides false) {
                MarkdownDocumentPreview(content, Modifier.weight(1f).fillMaxWidth(), documentKey = "comments:${item.key}",
                    onCopySource = { onCopySource(content) }, sourcePath = document.path,
                    onCopyPath = onCopyPath, onCopyFile = onCopyFile, onCopyCode = onCopySource,
                    fileCopyLabel = "复制评论缓存文件", fileCopyEnabled = !reader.loading)
            }
        }
    }
}

/** The cached text is complete; formatting is only a view and is never a second persisted copy. */
internal fun requirementCommentsMarkdown(data: RequirementComments): String = buildString {
    val byId = data.comments.associateBy { it.id }
    data.comments.forEachIndexed { index, comment ->
        if (index > 0) append("\n\n---\n\n")
        append("### ").append(escapeCommentLabel(comment.authorName.ifBlank { comment.authorKey.ifBlank { "未知用户" } }))
            .append(" · ").append(escapeCommentLabel(comment.createdAt.ifBlank { "时间未知" })).append("\n\n")
        comment.parentId?.let { parent ->
            append("回复：").append(escapeCommentLabel(byId[parent]?.authorName ?: parent)).append("\n\n")
        }
        append(comment.content.ifBlank { "（无文字内容）" })
        comment.attachmentUrl?.let { url ->
            val target = runCatching { URI(url).takeIf { it.scheme?.lowercase() in setOf("http", "https") && it.host != null } }.getOrNull()
            append("\n\n")
            if (target != null) append("[查看评论附件](<").append(target.toASCIIString().replace("<", "%3C").replace(">", "%3E")).append(">)")
            else append("附件：").append(escapeCommentLabel(url))
        }
    }
}

private fun escapeCommentLabel(text: String) = buildString {
    text.forEach { char ->
        if (char in "\\`*_{}[]<>#|!()") append('\\')
        append(if (char == '\n' || char == '\r') ' ' else char)
    }
}
