package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.ParticipatedWorkItem
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class RequirementDocumentStoreTest {
    @TempDir lateinit var root: Path
    private val item = ParticipatedWorkItem("project", "obt", "userstory", "需求", "711849", "支付渠道优化", "https://project.feishu.cn/obt/userstory/detail/711849")
    private val key = requirementReadKey("body", "account", requirementBodyIdentity(item))
    private val png = byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10, 1, 2, 3)
    private fun download(bytes: ByteArray = png) = root.resolve("remote.image").also { Files.write(it, bytes) }

    @Test fun `Markdown alone contains complete long body with title and original link`() {
        val body = "# 原文\n\n| 字段 | 内容 |\n|---|---|\n| 一 | 二 |\n\n" + "完整正文🙂".repeat(100_000)
        val store = RequirementDocumentStore(root.resolve("中文 缓存"))
        val document = store.write(key, item, body, 123)
        assertEquals(body, RequirementDocumentStore(root.resolve("中文 缓存")).read(key)!!.content)
        val md = Files.readString(document.markdownPath)
        assertTrue(md.startsWith("# 支付渠道优化")); assertTrue(item.url in md); assertTrue(md.endsWith(body))
        val index = Files.readString(document.markdownPath.parent.resolve("index.json"))
        assertFalse("完整正文" in index); assertFalse("content" in index); assertFalse("body\"" in index)
        assertTrue(document.complete); assertEquals(listOf(document.markdownPath), document.copyPaths())
    }

    @Test fun `inline reference and repeated images round trip while code stays literal`() {
        val url = "https://example.com/a.png?x=1&y=2"
        val body = "![一](<$url> \"标题\")\n\n![二][图片]\n\n[图片]: $url\n\n![三]($url)\n\n```md\n![代码](https://example.com/not-image.png)\n```"
        assertEquals(listOf(url), requirementDocumentImageUrls(body))
        val store = RequirementDocumentStore(root.resolve("docs"))
        val doc = store.write(key, item, body, 1)
        val saved = store.saveImage(key, doc.generation, url, download())!!
        assertEquals(body, saved.content)
        assertTrue(saved.complete); assertTrue(saved.images.getValue(url).toString().endsWith(".png"))
        val md = Files.readString(saved.markdownPath)
        assertFalse(url in md, md); assertTrue("![代码](https://example.com/not-image.png)" in md)
        assertEquals(listOf(saved.markdownPath, saved.assetsDirectory), saved.copyPaths())
        val export = root.resolve("粘贴到 中文目录")
        Files.createDirectories(export)
        Files.copy(saved.markdownPath, export.resolve(saved.markdownPath.fileName))
        Files.walk(saved.assetsDirectory).use { paths -> paths.forEach { path ->
            val target = export.resolve(saved.assetsDirectory.fileName).resolve(saved.assetsDirectory.relativize(path))
            if (Files.isDirectory(path)) Files.createDirectories(target) else Files.copy(path, target)
        } }
        val relative = saved.markdownPath.parent.relativize(saved.images.getValue(url))
        assertContentEquals(png, Files.readAllBytes(export.resolve(relative)))
        Files.delete(saved.images.getValue(url))
        assertContentEquals(png, Files.readAllBytes(export.resolve(relative)))
        assertFalse(store.read(key)!!.complete)
    }

    @Test fun `identical bytes at distinct URLs still restore original URLs exactly`() {
        val urls = listOf("https://example.com/one", "https://example.com/two")
        val body = urls.joinToString("\n\n") { "![图片]($it)" }
        val store = RequirementDocumentStore(root.resolve("docs"))
        val doc = store.write(key, item, body, 1)
        urls.forEach { store.saveImage(key, doc.generation, it, download()) }
        val saved = store.read(key)!!
        assertEquals(body, saved.content); assertTrue(saved.complete); assertEquals(2, saved.images.values.distinct().size)
    }

    @Test fun `image refresh uses content hash and failure preserves previous image`() {
        val url = "https://example.com/image"
        val store = RequirementDocumentStore(root.resolve("docs"))
        val body = "![图片]($url)"
        val first = store.write(key, item, body, 1)
        val old = store.saveImage(key, first.generation, url, download())!!
        val refreshed = store.write(key, item, body, 2)
        assertNull(store.saveImage(key, first.generation, url, download(png + 4)))
        val failure = store.failImage(key, refreshed.generation, url, "网络失败")!!
        assertEquals(old.images, failure.images); assertTrue(failure.complete); assertEquals("网络失败", failure.imageFailures[url])
        val new = store.saveImage(key, refreshed.generation, url, download(png + 5))!!
        assertNotEquals(old.images[url], new.images[url]); assertTrue(new.imageFailures.isEmpty()); assertEquals(body, new.content)
    }

    @Test fun `empty body valid account isolation and unsafe id rejection`() {
        val store = RequirementDocumentStore(root.resolve("docs"))
        assertEquals("", store.write(key, item, "", 1).content)
        assertNull(store.read(requirementReadKey("body", "other-account", requirementBodyIdentity(item))))
        assertFailsWith<IllegalArgumentException> { store.write("unsafe", item.copy(id = "../../outside"), "bad", 1) }
    }

    @Test fun `index replacement failure restores previous Markdown`() {
        val store = RequirementDocumentStore(root.resolve("docs"))
        val old = store.write(key, item, "旧正文", 1)
        val index = old.markdownPath.parent.resolve("index.json")
        Files.delete(index); Files.createDirectory(index); Files.writeString(index.resolve("obstacle"), "keep")
        assertFails { store.write(key, item, "新正文", 2) }
        assertTrue(Files.readString(old.markdownPath).endsWith("旧正文"))
        assertTrue(Files.exists(index.resolve("obstacle")))
    }
}
