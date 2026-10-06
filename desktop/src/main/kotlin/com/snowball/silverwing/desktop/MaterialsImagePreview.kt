package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ChevronLeft
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material.icons.outlined.Fullscreen
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.painter.BitmapPainter
import androidx.compose.ui.graphics.toComposeImageBitmap
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runInterruptible
import org.jetbrains.skia.Codec
import org.jetbrains.skia.Data
import org.jetbrains.skia.EncodedOrigin
import org.jetbrains.skia.Image
import org.jetbrains.skia.Surface
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path

internal val MATERIALS_IMAGE_EXTENSIONS = setOf("png", "jpg", "jpeg", "webp", "gif", "bmp", "ico")

/** Inspect encoded dimensions before allocating pixels; only the first frame is read. */
internal class MaterialsImageService(
    private val maxBytes: Int = 32 * 1024 * 1024,
    private val maxPixels: Long = 20_000_000,
) {
    fun read(root: Path, relativePath: String): ImageBitmap {
        require(RequirementMaterialsMarkdownFile(relativePath, 0).image) { "该格式不支持图片预览。" }
        val path = RequirementMaterialsMarkdownService().resolveOpenFile(root, relativePath)
        val limit = "图片超过预览大小限制，请使用外部应用打开。"
        require(Files.size(path) <= maxBytes) { limit }
        val bytes = Files.newInputStream(path, LinkOption.NOFOLLOW_LINKS).use { it.readNBytes(maxBytes + 1) }
        require(bytes.size <= maxBytes) { limit }
        return try {
            Data.makeFromBytes(bytes).use { data ->
                Codec.makeFromData(data).use { codec ->
                    require(codec.width > 0 && codec.height > 0 && codec.width.toLong() * codec.height <= maxPixels) { limit }
                    // Codec.readPixels() defaults to frame zero, which gives animated GIFs a
                    // predictable and bounded first-frame preview without retaining animation state.
                    codec.readPixels().use { bitmap ->
                        Image.makeFromBitmap(bitmap).use { image ->
                            val origin = codec.encodedOrigin
                            if (origin == EncodedOrigin.UNUSED || origin == EncodedOrigin.TOP_LEFT) {
                                image.toComposeImageBitmap()
                            } else {
                                // JPEG EXIF orientation is metadata on the encoded stream. Skia's
                                // raw Codec pixels do not apply it, so rasterize the oriented image
                                // once before handing the independent bitmap to Compose.
                                val outputWidth = if (origin.swapsWidthHeight()) codec.height else codec.width
                                val outputHeight = if (origin.swapsWidthHeight()) codec.width else codec.height
                                Surface.makeRasterN32Premul(outputWidth, outputHeight).use { surface ->
                                    surface.canvas.setMatrix(origin.toMatrix(codec.width, codec.height))
                                    surface.canvas.drawImage(image, 0f, 0f)
                                    surface.makeImageSnapshot().use { snapshot -> snapshot.toComposeImageBitmap() }
                                }
                            }
                        }
                    }
                }
            }
        } catch (error: IllegalArgumentException) {
            if (error.message == limit) throw error
            throw IllegalArgumentException("无法解码图片，请重试或使用外部应用打开。", error)
        } catch (error: Exception) {
            throw IllegalArgumentException("无法解码图片，请重试或使用外部应用打开。", error)
        }
    }
}

private sealed interface MaterialsImageState {
    data object Loading : MaterialsImageState
    data class Loaded(val image: ImageBitmap) : MaterialsImageState
    data class Failed(val message: String) : MaterialsImageState
}

@Composable
internal fun MaterialsImagePreview(
    root: Path, relativePath: String, refreshKey: Any,
    images: List<String>, onNavigate: (String) -> Unit,
    modifier: Modifier = Modifier, ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {
    var state by remember(root, relativePath, refreshKey) { mutableStateOf<MaterialsImageState>(MaterialsImageState.Loading) }
    var retry by remember(root, relativePath) { mutableIntStateOf(0) }
    var expanded by remember(root, relativePath) { mutableStateOf(false) }
    LaunchedEffect(root, relativePath, refreshKey, retry) {
        state = MaterialsImageState.Loading
        try { state = MaterialsImageState.Loaded(runInterruptible(ioDispatcher) { MaterialsImageService().read(root, relativePath) }) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { state = MaterialsImageState.Failed(error.message ?: "无法读取图片。") }
    }
    when (val current = state) {
        MaterialsImageState.Loading -> Column(modifier.padding(16.dp)) {
            LinearProgressIndicator(Modifier.fillMaxWidth()); Text("正在读取图片…")
        }
        is MaterialsImageState.Failed -> Column(modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(current.message, color = MaterialTheme.colorScheme.error)
            TextButton(onClick = { retry++ }) { Text("重试图片") }
        }
        is MaterialsImageState.Loaded -> {
            val painter = remember(current.image) { BitmapPainter(current.image) }
            val index = images.indexOf(relativePath)
            Column(modifier) {
                FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                    ActionIconButton("上一张图片", { images.getOrNull(index - 1)?.let(onNavigate) }, enabled = index > 0) {
                        Icon(Icons.Outlined.ChevronLeft, null)
                    }
                    Text("${index + 1} / ${images.size} · ${current.image.width} × ${current.image.height}",
                        Modifier.padding(vertical = 12.dp), style = MaterialTheme.typography.labelMedium)
                    ActionIconButton("下一张图片", { images.getOrNull(index + 1)?.let(onNavigate) }, enabled = index >= 0 && index < images.lastIndex) {
                        Icon(Icons.Outlined.ChevronRight, null)
                    }
                    ActionIconButton("放大查看图片", { expanded = true }) { Icon(Icons.Outlined.Fullscreen, null) }
                }
                RequirementImageViewerContent(painter, null, Modifier.weight(1f).fillMaxWidth(), title = "图片预览")
            }
            if (expanded) RequirementImageViewer(painter, { expanded = false })
        }
    }
}
