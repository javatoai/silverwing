package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.Json
import java.nio.ByteBuffer
import java.nio.channels.FileChannel
import java.nio.file.Files
import java.nio.charset.StandardCharsets
import java.nio.file.StandardOpenOption
import java.time.Clock
import java.time.Instant
import kotlin.io.path.createDirectories
import kotlin.io.path.exists
import kotlin.io.path.name

@Serializable
data class ApplicationEvent(
    val timestamp: String,
    val level: String,
    val event: String,
    val message: String,
    val metadata: Map<String, String> = emptyMap(),
)

object ApplicationEventClipboard {
    fun format(event: ApplicationEvent): String = buildString {
        append(event.timestamp)
        append(" · ")
        append(event.event)
        append('\n')
        append(event.message)
        event.metadata.toSortedMap().forEach { (key, value) ->
            append('\n')
            append(key)
            append('=')
            append(value)
        }
    }

    fun latest(events: List<ApplicationEvent>): String? = events.firstOrNull()?.let(::format)

    fun all(events: List<ApplicationEvent>): String = events.joinToString("\n\n", transform = ::format)
}

fun interface EventSink {
    fun record(event: ApplicationEvent)
}

object NoOpEventSink : EventSink {
    override fun record(event: ApplicationEvent) = Unit
}

class JsonlEventSink(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val clock: Clock = Clock.systemUTC(),
) : EventSink {
    private val json = Json { encodeDefaults = true }
    private val monitor = Any()

    override fun record(event: ApplicationEvent) {
        // Logging must never change the outcome of a Git operation.
        runCatching {
            synchronized(monitor) {
                paths.logs.createDirectories()
                val date = SilverWingTime.localDate(Instant.now(clock))
                val bytes = (json.encodeToString(event) + System.lineSeparator())
                    .toByteArray(StandardCharsets.UTF_8)
                FileChannel.open(
                    paths.logs.resolve("application-$date.jsonl"),
                    StandardOpenOption.CREATE,
                    StandardOpenOption.WRITE,
                    StandardOpenOption.APPEND,
                ).use { channel ->
                    channel.lock().use {
                        val buffer = ByteBuffer.wrap(bytes)
                        while (buffer.hasRemaining()) channel.write(buffer)
                        channel.force(false)
                    }
                }
            }
        }
    }
}

class ApplicationErrorLogReader(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val json: Json = Json { ignoreUnknownKeys = true },
) {
    fun latest(limit: Int = 10): List<ApplicationEvent> {
        require(limit > 0) { "错误日志条数必须大于 0" }
        if (!paths.logs.exists()) return emptyList()
        return Files.list(paths.logs).use { files ->
            files
                .filter { it.name.matches(Regex("application-\\d{4}-\\d{2}-\\d{2}\\.jsonl")) }
                .sorted(Comparator.reverseOrder())
                .toList()
                .asSequence()
                .flatMap { file -> Files.readAllLines(file).asReversed().asSequence() }
                .mapNotNull { line -> runCatching { json.decodeFromString<ApplicationEvent>(line) }.getOrNull() }
                .filter { it.level == "ERROR" }
                .take(limit)
                .toList()
        }
    }
}

fun EventSink.info(
    event: String,
    message: String,
    metadata: Map<String, String> = emptyMap(),
    clock: Clock = Clock.systemUTC(),
) {
    record(
        ApplicationEvent(
            timestamp = SilverWingTime.format(Instant.now(clock)),
            level = "INFO",
            event = event,
            message = message,
            metadata = metadata,
        ),
    )
}

fun EventSink.error(
    event: String,
    message: String,
    metadata: Map<String, String> = emptyMap(),
    clock: Clock = Clock.systemUTC(),
) {
    record(
        ApplicationEvent(
            timestamp = SilverWingTime.format(Instant.now(clock)),
            level = "ERROR",
            event = event,
            message = message,
            metadata = metadata,
        ),
    )
}
