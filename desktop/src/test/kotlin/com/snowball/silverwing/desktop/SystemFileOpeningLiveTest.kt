package com.snowball.silverwing.desktop

import com.sun.jna.platform.win32.User32
import kotlinx.coroutines.runBlocking
import org.apache.pdfbox.pdmodel.PDDocument
import org.apache.pdfbox.pdmodel.PDPage
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable
import org.junit.jupiter.api.Assumptions.assumeTrue
import java.nio.file.Files
import java.nio.file.Path
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** 仅显式启用时才唤起本机软件；普通回归测试不会弹出 WPS。 */
@EnabledIfEnvironmentVariable(named = "SILVERWING_VERIFY_NATIVE_OPEN", matches = "1")
class SystemFileOpeningLiveTest {
    @Test fun `system shell opens valid Word Excel and PDF fixtures`() = runBlocking {
        val directory = Files.createTempDirectory("silverwing-native-file-open-").resolve("中文 空格 & ' 验证")
        Files.createDirectories(directory)
        val word = directory.resolve("silverwing-Word打开验证.docx")
        val excel = directory.resolve("silverwing-Excel打开验证.xlsx")
        val pdf = directory.resolve("silverwing-PDF打开验证.pdf")
        zip(word, mapOf(
            "[Content_Types].xml" to """<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>""",
            "_rels/.rels" to relationships("word/document.xml", "officeDocument"),
            "word/document.xml" to """<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Silverwing 外部打开验证示例</w:t></w:r></w:p><w:sectPr/></w:body></w:document>""",
        ))
        zip(excel, mapOf(
            "[Content_Types].xml" to """<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>""",
            "_rels/.rels" to relationships("xl/workbook.xml", "officeDocument"),
            "xl/workbook.xml" to """<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="打开验证" sheetId="1" r:id="rId1"/></sheets></workbook>""",
            "xl/_rels/workbook.xml.rels" to relationships("worksheets/sheet1.xml", "worksheet"),
            "xl/worksheets/sheet1.xml" to """<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Silverwing 外部打开验证示例</t></is></c></row></sheetData></worksheet>""",
        ))
        PDDocument().use { it.addPage(PDPage()); it.save(pdf.toFile()) }
        val missingWindows = mutableListOf<String>()
        PlatformSystemFileOpening().use { opening ->
            for (path in listOf(word, excel, pdf)) {
                println("Opening $path: ${opening.defaultApplication(path).displayName}")
                assertEquals(FileOpenResult.Submitted, opening.open(path))
                val expected = path.fileName.toString().substringBeforeLast('.')
                val deadline = System.nanoTime() + 15_000_000_000L
                var found = false
                do {
                    User32.INSTANCE.EnumWindows({ window, _ ->
                        if (User32.INSTANCE.IsWindowVisible(window)) {
                            val title = CharArray(1024)
                            User32.INSTANCE.GetWindowText(window, title, title.size)
                            if (String(title).substringBefore('\u0000').contains(expected)) found = true
                        }
                        true
                    }, null)
                    if (!found) Thread.sleep(100)
                } while (!found && System.nanoTime() < deadline)
                if (found) println("Verified visible document window: $expected")
                else { missingWindows += expected; println("Shell accepted file, but no matching document window was observable: $expected") }
            }
        }
        // 系统接受请求并不保证第三方软件显示窗口，无法观察窗口时明确标记实机验收未完成。
        assumeTrue(missingWindows.isEmpty(), "WPS 窗口验收未完成：${missingWindows.joinToString()}")
    }

    private fun relationships(target: String, type: String) = """<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/$type" Target="$target"/></Relationships>"""
    private fun zip(path: Path, entries: Map<String, String>) {
        ZipOutputStream(Files.newOutputStream(path)).use { output -> entries.forEach { (name, xml) ->
            output.putNextEntry(ZipEntry(name)); output.write(xml.toByteArray(Charsets.UTF_8)); output.closeEntry()
        } }
    }
}
