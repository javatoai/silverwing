package com.snowball.silverwing.desktop

import io.github.vinceglb.filekit.FileKit
import io.github.vinceglb.filekit.PlatformFile
import io.github.vinceglb.filekit.dialogs.FileKitType
import io.github.vinceglb.filekit.dialogs.openDirectoryPicker
import io.github.vinceglb.filekit.dialogs.openFilePicker
import java.io.File

/**
 * Desktop port for native path dialogs. Callers receive `null` on cancellation,
 * allowing editors to retain the current value without special UI branching.
 */
interface NativePathPicker {
    suspend fun pickDirectory(initialPath: String? = null): String?
    suspend fun pickDirectories(initialPath: String? = null): List<String>?
    suspend fun pickFile(initialPath: String? = null, extensions: List<String> = emptyList()): String?
    suspend fun pickApplication(initialPath: String? = null): String?
}

class FileKitNativePathPicker : NativePathPicker {
    override suspend fun pickDirectory(initialPath: String?): String? = FileKit
        .openDirectoryPicker(directory = initialPath.asInitialPlatformFile())
        ?.file
        ?.absolutePath

    override suspend fun pickDirectories(initialPath: String?): List<String>? = pickDirectoriesForOs(
        osName = System.getProperty("os.name"),
        initialPath = initialPath,
        windowsPicker = { WindowsMultiDirectoryPicker.pick(it) },
        macPicker = { MacMultiDirectoryPicker.pick(it) },
        singlePicker = { path -> pickDirectory(path)?.let(::listOf) },
    )

    override suspend fun pickFile(initialPath: String?, extensions: List<String>): String? = FileKit
        .openFilePicker(
            type = FileKitType.File(extensions),
            directory = initialPath.asInitialDirectory(),
        )
        ?.file
        ?.absolutePath

    override suspend fun pickApplication(initialPath: String?): String? =
        // macOS treats .app bundles as files in an extension-filtered NSOpenPanel;
        // a directory picker would descend into the bundle instead of selecting it.
        pickFile(initialPath, applicationPickerExtensions(System.getProperty("os.name")))

    private fun String?.asInitialPlatformFile(): PlatformFile? =
        this?.trim()?.takeIf(String::isNotEmpty)?.let(::File)?.let(::PlatformFile)

    private fun String?.asInitialDirectory(): PlatformFile? = this
        ?.trim()
        ?.takeIf(String::isNotEmpty)
        ?.let(::File)
        ?.let { selected -> if (selected.isDirectory) selected else selected.parentFile ?: selected }
        ?.let(::PlatformFile)
}

/** Application pickers filter to .app bundles only on macOS; Windows keeps any file. */
internal fun applicationPickerExtensions(osName: String): List<String> =
    if (isMacOs(osName)) listOf("app") else emptyList()

internal fun isMacOs(osName: String): Boolean = osName.startsWith("Mac", ignoreCase = true)

internal suspend fun pickDirectoriesForOs(
    osName: String,
    initialPath: String?,
    windowsPicker: suspend (String?) -> List<String>?,
    macPicker: suspend (String?) -> List<String>?,
    singlePicker: suspend (String?) -> List<String>?,
): List<String>? = when {
    osName.startsWith("Windows", ignoreCase = true) -> windowsPicker(initialPath)
    isMacOs(osName) -> macPicker(initialPath)
    else -> singlePicker(initialPath)
}

/** Small testable coordinator that guarantees cancellation never clears a field. */
class PathSelectionCoordinator(
    private val picker: NativePathPicker,
) {
    suspend fun directory(currentValue: String, initialPath: String? = currentValue): String =
        picker.pickDirectory(initialPath) ?: currentValue

    suspend fun file(currentValue: String, initialPath: String? = currentValue): String =
        picker.pickFile(initialPath) ?: currentValue

    suspend fun application(currentValue: String, initialPath: String? = currentValue): String =
        picker.pickApplication(initialPath) ?: currentValue

    suspend fun directories(initialPath: String? = null): List<String> =
        picker.pickDirectories(initialPath).orEmpty()
}
