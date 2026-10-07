package com.snowball.silverwing.desktop

import com.sun.net.httpserver.HttpServer
import org.junit.jupiter.api.Test
import java.net.InetSocketAddress
import java.nio.file.Files
import kotlin.test.*

class RequirementHttpImageTest {
    @Test fun `public HTTP image downloads actual bytes while error pages never become images`() {
        val bytes = byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10, 1)
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/image") { exchange ->
            exchange.responseHeaders.add("Content-Type", "application/octet-stream")
            exchange.sendResponseHeaders(200, bytes.size.toLong())
            exchange.responseBody.use { it.write(bytes) }
        }
        server.createContext("/html") { exchange ->
            val html = "<html>login</html>".toByteArray()
            exchange.sendResponseHeaders(200, html.size.toLong()); exchange.responseBody.use { it.write(html) }
        }
        server.createContext("/failed") { exchange -> exchange.sendResponseHeaders(403, -1); exchange.close() }
        server.start()
        try {
            val base = "http://127.0.0.1:${server.address.port}"
            val image = downloadRequirementHttpImage("$base/image")
            try { assertContentEquals(bytes, Files.readAllBytes(image)); assertEquals("png", requirementImageExtension(Files.readAllBytes(image))) }
            finally { Files.deleteIfExists(image) }
            assertFailsWith<IllegalStateException> { downloadRequirementHttpImage("$base/html") }
            assertContains(assertFailsWith<IllegalStateException> { downloadRequirementHttpImage("$base/failed") }.message!!, "403")
        } finally { server.stop(0) }
    }

    @Test fun `non web protocols and embedded credentials are rejected before network access`() {
        for (url in listOf("file:///C:/private.png", "ftp://example.com/a.png", "https://user:password@example.com/a.png")) {
            assertFailsWith<IllegalArgumentException> { downloadRequirementHttpImage(url) }
        }
    }
}
