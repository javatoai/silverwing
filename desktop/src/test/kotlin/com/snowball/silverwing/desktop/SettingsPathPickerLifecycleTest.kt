@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Path
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class SettingsPathPickerLifecycleTest {
    @TempDir lateinit var temporary: Path

    @Test fun `duplicate picker clicks are ignored and failed selection callback permits retry`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val gate = CompletableDeferred<String?>()
        var calls = 0
        val picker = picker { calls++; if (calls == 1) gate.await() else "C:/资料 有空格 & #/重试" }
        try {
            application(dispatcher, picker).use { app ->
                app.chooseDirectory { error("无法应用所选路径") }
                app.chooseDirectory { fail("Repeated click must not start a second picker") }
                assertTrue(app.pathPickerBusy)
                runCurrent()
                assertEquals(1, calls)
                gate.complete("C:/资料 有空格 & #/首次")
                runCurrent()
                assertFalse(app.pathPickerBusy)
                assertContains(app.errorMessage.orEmpty(), "无法应用所选路径")
                app.dismissMessages()
                var selected: String? = null
                app.chooseFile { selected = it }
                runCurrent()
                assertEquals("C:/资料 有空格 & #/重试", selected)
                assertEquals(2, calls)
                assertFalse(app.pathPickerBusy)
                assertNull(app.errorMessage)
            }
        } finally { Dispatchers.resetMain() }
    }

    @Test fun `picker failures cancellations and null selection retain input and permit retry`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        var calls = 0
        val picker = picker {
            when (++calls) {
                1 -> error("系统选择窗口失败")
                2 -> throw CancellationException("cancelled")
                3 -> null
                else -> "C:/资料/最终选择"
            }
        }
        try {
            application(dispatcher, picker).use { app ->
                var field = "原有输入"
                app.chooseDirectory { field = it }; runCurrent()
                assertContains(app.errorMessage.orEmpty(), "系统选择窗口失败")
                assertFalse(app.pathPickerBusy)
                app.dismissMessages()
                repeat(2) {
                    app.chooseDirectory { field = it }; runCurrent()
                    assertNull(app.errorMessage)
                    assertEquals("原有输入", field)
                    assertFalse(app.pathPickerBusy)
                }
                app.chooseApplication { field = it }; runCurrent()
                assertEquals("C:/资料/最终选择", field)
                assertFalse(app.pathPickerBusy)
                assertNull(app.errorMessage)
            }
        } finally { Dispatchers.resetMain() }
    }

    @Test fun `scope cancellation before picker coroutine starts clears busy and never calls native picker`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        var calls = 0
        try {
            val app = application(dispatcher, picker { calls++; "unused" })
            app.chooseDirectory { fail("Cancelled picker must not update a field") }
            assertTrue(app.pathPickerBusy)
            app.close()
            runCurrent()
            assertEquals(0, calls)
            assertFalse(app.pathPickerBusy)
            app.chooseDirectory { fail("Closed scope must not update a field") }
            assertFalse(app.pathPickerBusy)
        } finally { Dispatchers.resetMain() }
    }

    private fun picker(select: suspend () -> String?): NativePathPicker = object : NativePathPicker {
        override suspend fun pickDirectory(initialPath: String?) = select()
        override suspend fun pickDirectories(initialPath: String?) = select()?.let(::listOf)
        override suspend fun pickFile(initialPath: String?, extensions: List<String>) = select()
        override suspend fun pickApplication(initialPath: String?) = select()
    }

    private fun application(dispatcher: CoroutineDispatcher, picker: NativePathPicker): DesktopApplication {
        val paths = ApplicationPaths(temporary.resolve("home"))
        val store = ConfigStore(paths).also { it.save(AppConfig(aiRequirementNamingEnabled = false)) }
        return DesktopApplication(paths = paths, configStore = store, nativePathPicker = picker,
            ioDispatcher = dispatcher,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
    }
}
