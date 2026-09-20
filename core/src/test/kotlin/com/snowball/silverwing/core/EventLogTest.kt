package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset

class EventLogTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `writes one json object per event without source contents`() {
        val clock = Clock.fixed(Instant.parse("2026-07-30T16:00:00Z"), ZoneOffset.UTC)
        val paths = ApplicationPaths(temporary.resolve("home"))
        val sink = JsonlEventSink(paths, clock)

        sink.info(
            event = "task.create.completed",
            message = "任务创建完成",
            metadata = mapOf("folderName" to "OBT-1"),
            clock = clock,
        )

        val log = paths.logs.resolve("application-2026-07-31.jsonl")
        assertTrue(Files.exists(log))
        val lines = Files.readAllLines(log)
        assertEquals(1, lines.size)
        assertTrue(lines.single().contains("\"folderName\":\"OBT-1\""))
        assertTrue(lines.single().contains("\"event\":\"task.create.completed\""))
        assertTrue(lines.single().contains("\"timestamp\":\"2026-07-31 00:00:00\""))
    }

    @Test
    fun `reader returns latest ten errors across daily files and ignores malformed lines`() {
        val paths = ApplicationPaths(temporary.resolve("reader-home"))
        Files.createDirectories(paths.logs)
        Files.writeString(paths.logs.resolve("application-2026-07-30.jsonl"), "broken\n")
        val clock = Clock.fixed(Instant.parse("2026-07-30T16:00:00Z"), ZoneOffset.UTC)
        val sink = JsonlEventSink(paths, clock)
        repeat(12) { index -> sink.error("test.error", "error-$index", clock = clock) }
        sink.info("test.info", "ignored", clock = clock)

        val errors = ApplicationErrorLogReader(paths).latest(10)

        assertEquals((2..11).map { "error-$it" }.reversed(), errors.map { it.message })
    }

    @Test
    fun `clipboard formatter can copy only the newest complete error`() {
        val newest = ApplicationEvent(
            timestamp = "2026-08-12 10:30:00",
            level = "ERROR",
            event = "task.create.failed",
            message = "Git fetch failed",
            metadata = mapOf("service" to "api-service"),
        )
        val older = newest.copy(timestamp = "2026-08-12 10:20:00", message = "older")

        assertEquals(
            "2026-08-12 10:30:00 · task.create.failed\nGit fetch failed\nservice=api-service",
            ApplicationEventClipboard.format(newest),
        )
        assertEquals(ApplicationEventClipboard.format(newest), ApplicationEventClipboard.latest(listOf(newest, older)))
    }
}
