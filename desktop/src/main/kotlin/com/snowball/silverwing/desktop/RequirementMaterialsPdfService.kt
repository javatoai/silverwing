package com.snowball.silverwing.desktop

import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.toComposeImageBitmap
import org.apache.pdfbox.Loader
import org.apache.pdfbox.pdmodel.encryption.InvalidPasswordException
import org.apache.pdfbox.rendering.ImageType
import org.apache.pdfbox.rendering.PDFRenderer
import java.io.IOException
import java.nio.file.Path
import kotlin.math.min
import kotlin.math.sqrt

internal const val MAX_PDF_PREVIEW_PAGES = 10_000

internal data class RequirementMaterialsPdfPage(
    val pageIndex: Int,
    val pageCount: Int,
    val image: ImageBitmap,
    val aspectRatio: Float,
)

/** 打开后只保留有界的原始文件和页面尺寸，页面位图由可见区域按需请求。 */
internal class RequirementMaterialsPdfService(
    private val files: RequirementMaterialsMarkdownService = RequirementMaterialsMarkdownService(),
) {
    fun open(root: Path, relativePath: String): RequirementMaterialsPdfDocument {
        checkPdfInterrupted()
        val bytes = files.readPdf(root, relativePath)
        val dimensions = withPdfDocument(bytes) { document ->
            require(document.numberOfPages > 0) { "PDF 没有可预览的页面。" }
            require(document.numberOfPages <= MAX_PDF_PREVIEW_PAGES) { "PDF 页数较多，请使用外部阅读器预览。" }
            document.pages.map { page ->
                checkPdfInterrupted()
                val box = page.cropBox
                val rotated = Math.floorMod(page.rotation, 180) == 90
                val width = if (rotated) box.height else box.width
                val height = if (rotated) box.width else box.height
                pdfRenderScale(width, height, 1) // 提前校验页面尺寸，保证滚动占位高度正确。
                width to height
            }
        }
        return RequirementMaterialsPdfDocument(bytes, dimensions)
    }

    fun render(root: Path, relativePath: String, pageIndex: Int, widthPixels: Int): RequirementMaterialsPdfPage =
        open(root, relativePath).render(pageIndex, widthPixels)
}

internal class RequirementMaterialsPdfDocument(
    private val bytes: ByteArray,
    private val dimensions: List<Pair<Float, Float>>,
) {
    val pageCount: Int get() = dimensions.size
    fun aspectRatio(index: Int): Float = dimensions[index].let { (width, height) -> width / height }

    fun textIndex(): PdfTextIndex {
        checkPdfInterrupted()
        return withPdfDocument(bytes) { document -> readPdfTextIndex(document) }
    }

    /** 不共享 PDFBox 文档对象，渲染完成或取消后立即释放解析器与图片资源。 */
    fun render(pageIndex: Int, widthPixels: Int): RequirementMaterialsPdfPage {
        require(pageIndex >= 0) { "PDF 页码无效" }
        require(widthPixels > 0) { "PDF 预览宽度无效" }
        checkPdfInterrupted()
        return withPdfDocument(bytes) { document ->
            val index = pageIndex.coerceAtMost(pageCount - 1)
            val (width, height) = dimensions[index]
            val renderer = PDFRenderer(document).apply { isSubsamplingAllowed = true }
            val rendered = renderer.renderImage(index, pdfRenderScale(width, height, widthPixels), ImageType.RGB)
            try {
                checkPdfInterrupted()
                RequirementMaterialsPdfPage(index, pageCount, rendered.toComposeImageBitmap(), width / height)
            } finally {
                rendered.flush()
            }
        }
    }
}

internal fun checkPdfInterrupted() {
    if (Thread.currentThread().isInterrupted) throw InterruptedException("PDF 预览已取消")
}

private inline fun <T> withPdfDocument(bytes: ByteArray, action: (org.apache.pdfbox.pdmodel.PDDocument) -> T): T =
    try {
        Loader.loadPDF(bytes).use(action)
    } catch (error: InvalidPasswordException) {
        throw IllegalArgumentException("PDF 需要密码，暂不支持预览。请使用外部阅读器打开。", error)
    } catch (error: IOException) {
        throw IllegalArgumentException("PDF 无法读取，文件可能已损坏或格式不受支持。", error)
    }

/** 扫描件与异常页面也只能产生有界位图；缩放显示仍可放大，不无限分配像素。 */
internal fun pdfRenderScale(width: Float, height: Float, requestedWidth: Int): Float {
    require(width.isFinite() && height.isFinite() && width > 0 && height > 0) { "PDF 页面尺寸无效。" }
    val requestedScale = requestedWidth.coerceIn(1, 8192).toDouble() / width
    val dimensionScale = 4096.0 / maxOf(width, height)
    val pixelScale = sqrt(4_000_000.0 / (width.toDouble() * height))
    val scale = min(requestedScale, min(dimensionScale, pixelScale)).toFloat()
    require(scale.isFinite() && scale > 0f && (width / height).isFinite() && width / height > 0f) {
        "PDF 页面尺寸无效。"
    }
    return scale
}
