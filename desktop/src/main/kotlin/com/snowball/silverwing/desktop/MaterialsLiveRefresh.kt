package com.snowball.silverwing.desktop

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.platform.LocalWindowInfo
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.channelFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.withTimeoutOrNull
import java.io.IOException
import java.io.InterruptedIOException
import java.nio.file.ClosedWatchServiceException
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.StandardWatchEventKinds
import java.nio.file.WatchKey
import java.nio.file.WatchService
import java.nio.file.attribute.BasicFileAttributes
import java.nio.file.attribute.FileTime
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

internal data class MaterialsLiveRefreshChange(
    val catalogChanged: Boolean,
    val selectedFileChanged: Boolean,
)

/**
 * Own this effect only while the materials browser is composed. It never holds document content,
 * reading positions, PDF documents or bitmaps. The callback runs on the caller's Compose context.
 */
@Composable
internal fun MaterialsLiveRefreshEffect(
    root: Path?,
    selectedRelativePath: String?,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    onChange: (MaterialsLiveRefreshChange) -> Unit,
) {
    val currentSelection by rememberUpdatedState(selectedRelativePath)
    val currentOnChange by rememberUpdatedState(onChange)
    val windowInfo = LocalWindowInfo.current
    LaunchedEffect(root, ioDispatcher) {
        if (root == null) return@LaunchedEffect
        MaterialsLiveRefreshService(ioDispatcher).changes(
            root,
            snapshotFlow { currentSelection },
            snapshotFlow { windowInfo.isWindowFocused }.filter { it }.map { Unit },
        ).collect { currentOnChange(it) }
    }
}

/**
 * Watch local directory events and recheck on window activation; no timer polls the filesystem.
 * All traversal and watcher registration happens on IO. A focus recheck also provides recovery
 * when a filesystem has no WatchService, overflows its queue, or misses an external editor's save.
 */
internal class MaterialsLiveRefreshService(
    private val ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    private val debounceMillis: Long = 200,
    private val watchServiceFactory: (Path) -> WatchService? = { root ->
        // UNC roots are checked on activation only; do not start an automatic network watch.
        if (root.toString().startsWith("\\\\")) null else root.fileSystem.newWatchService()
    },
) {
    init { require(debounceMillis in 1..1_000) }

    fun changes(
        rootPath: Path,
        selectedPaths: Flow<String?>,
        focusChecks: Flow<Unit>,
    ): Flow<MaterialsLiveRefreshChange> = channelFlow {
        val root = rootPath.toAbsolutePath().normalize()
        val checks = Channel<Unit>(Channel.CONFLATED)
        val selection = AtomicReference<String?>(null)
        val selectedTouched = AtomicBoolean(false)
        val watchedDirectories = ConcurrentHashMap<WatchKey, Path>()
        val watcher = try { watchServiceFactory(root) } catch (_: IOException) { null }
            catch (_: UnsupportedOperationException) { null }
            catch (_: SecurityException) { null }
        suspend fun registerDirectories(snapshot: MaterialsLiveRefreshSnapshot) = runInterruptible(ioDispatcher) {
            if (watcher == null) return@runInterruptible
            val wanted = snapshot.directories + listOfNotNull(materialsWatchAnchor(root))
            watchedDirectories.entries.toList().forEach { (key, directory) ->
                if (directory !in wanted || !key.isValid) {
                    watchedDirectories.remove(key)
                    key.cancel()
                }
            }
            val registered = watchedDirectories.values.toSet()
            wanted.filter { it !in registered }.forEach { directory ->
                checkMaterialsRefreshInterrupted()
                if (!materialsPathContainsSymlink(directory) &&
                    Files.isDirectory(directory, LinkOption.NOFOLLOW_LINKS)) {
                    try {
                        val key = directory.register(
                            watcher,
                            StandardWatchEventKinds.ENTRY_CREATE,
                            StandardWatchEventKinds.ENTRY_DELETE,
                            StandardWatchEventKinds.ENTRY_MODIFY,
                        )
                        watchedDirectories[key] = directory
                    } catch (_: IOException) {
                        // A concurrent rename/delete is recovered by the next event or activation.
                    } catch (_: UnsupportedOperationException) {
                        // Activation checks remain available on unsupported filesystems.
                    } catch (_: SecurityException) {
                        // Access may be revoked while the user is reading another window.
                    }
                }
            }
        }

        try {
            var previous = runInterruptible(ioDispatcher) { captureMaterialsLiveRefreshSnapshot(root) }
            registerDirectories(previous)
            launch {
                selectedPaths.distinctUntilChanged().collect { relativePath ->
                    selection.set(safeMaterialsRefreshRelativePath(relativePath))
                }
            }
            launch { focusChecks.collect { checks.trySend(Unit) } }
            if (watcher != null) launch {
                while (isActive) {
                    // take() must never run on a caller's test/UI dispatcher: it blocks until an
                    // event or cancellation. Catalog IO remains independently injectable.
                    val key = try { runInterruptible(Dispatchers.IO) { watcher.take() } }
                        catch (_: ClosedWatchServiceException) { break }
                    val directory = watchedDirectories[key]
                    var relevant = directory != null && !key.isValid
                    key.pollEvents().forEach { event ->
                        if (event.kind() == StandardWatchEventKinds.OVERFLOW) {
                            relevant = true
                            selectedTouched.set(true)
                        } else {
                            val relative = event.context() as? Path
                            if (directory != null && relative != null) {
                                val changed = directory.resolve(relative).normalize()
                                if (materialsRefreshEventIsRelevant(root, changed)) {
                                    relevant = true
                                    if (selection.get()?.let { root.resolve(it).normalize() } == changed) {
                                        // Editors can preserve size and timestamp; a watch event still
                                        // invalidates the selected preview in that case.
                                        selectedTouched.set(true)
                                    }
                                }
                            }
                        }
                    }
                    if (!key.reset()) {
                        watchedDirectories.remove(key)
                        relevant = true
                    }
                    if (relevant) checks.trySend(Unit)
                }
            }

            for (ignored in checks) {
                // Quiet-period debounce with a one-second ceiling for a continuously busy root.
                val burstStarted = System.nanoTime()
                while (System.nanoTime() - burstStarted < 1_000_000_000L &&
                    withTimeoutOrNull(debounceMillis) { checks.receive() } != null) { }
                val touched = selectedTouched.getAndSet(false)
                val next = runInterruptible(ioDispatcher) { captureMaterialsLiveRefreshSnapshot(root) }
                registerDirectories(next)
                val change = materialsLiveRefreshChange(previous, next, selection.get(), touched)
                previous = next
                if (change.catalogChanged || change.selectedFileChanged) send(change)
            }
        } finally {
            // Closing also wakes a blocked take() immediately when leaving this page or task.
            runCatching { watcher?.close() }
            checks.close()
        }
    }.flowOn(ioDispatcher)
}

internal data class MaterialsLiveRefreshFileStamp(
    val size: Long,
    val modified: FileTime,
    val created: FileTime,
    val fileKey: String?,
)

internal enum class MaterialsLiveRefreshRootState { DIRECTORY, MISSING, UNSAFE, UNAVAILABLE }

internal data class MaterialsLiveRefreshSnapshot(
    val rootState: MaterialsLiveRefreshRootState,
    val files: Map<String, MaterialsLiveRefreshFileStamp> = emptyMap(),
    val directories: Set<Path> = emptySet(),
)

/** Metadata only, without FOLLOW_LINKS; .git files and directories are both excluded. */
internal fun captureMaterialsLiveRefreshSnapshot(rootPath: Path): MaterialsLiveRefreshSnapshot {
    val root = rootPath.toAbsolutePath().normalize()
    checkMaterialsRefreshInterrupted()
    if (root.any { it.toString().equals(".git", ignoreCase = true) } || materialsPathContainsSymlink(root)) {
        return MaterialsLiveRefreshSnapshot(MaterialsLiveRefreshRootState.UNSAFE)
    }
    if (!Files.exists(root, LinkOption.NOFOLLOW_LINKS)) {
        return MaterialsLiveRefreshSnapshot(MaterialsLiveRefreshRootState.MISSING)
    }
    if (!Files.isDirectory(root, LinkOption.NOFOLLOW_LINKS)) {
        return MaterialsLiveRefreshSnapshot(MaterialsLiveRefreshRootState.UNSAFE)
    }
    val files = linkedMapOf<String, MaterialsLiveRefreshFileStamp>()
    val directories = linkedSetOf<Path>()
    try {
        val realRoot = root.toRealPath()
        Files.walkFileTree(root, object : SimpleFileVisitor<Path>() {
            override fun preVisitDirectory(directory: Path, attrs: BasicFileAttributes): FileVisitResult {
                checkMaterialsRefreshInterrupted()
                if (directory.fileName?.toString().equals(".git", ignoreCase = true) || isMaterialsStagingName(directory.fileName?.toString()) ||
                    attrs.isSymbolicLink || Files.isSymbolicLink(directory) ||
                    !directory.toRealPath().startsWith(realRoot)) {
                    return FileVisitResult.SKIP_SUBTREE
                }
                directories.add(directory)
                return FileVisitResult.CONTINUE
            }

            override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                checkMaterialsRefreshInterrupted()
                if (attrs.isRegularFile && !attrs.isSymbolicLink && !Files.isSymbolicLink(file) &&
                    !file.fileName.toString().equals(".git", ignoreCase = true) && !isMaterialsStagingName(file.fileName?.toString())) {
                    val relative = root.relativize(file).joinToString("/") { it.toString() }
                    files[relative] = MaterialsLiveRefreshFileStamp(
                        attrs.size(), attrs.lastModifiedTime(), attrs.creationTime(), attrs.fileKey()?.toString(),
                    )
                }
                return FileVisitResult.CONTINUE
            }

            override fun visitFileFailed(file: Path, exc: IOException): FileVisitResult {
                checkMaterialsRefreshInterrupted()
                // A file removed during the scan is simply absent from the refreshed catalog.
                if (exc is java.nio.file.NoSuchFileException) return FileVisitResult.CONTINUE
                throw exc
            }
        })
    } catch (interrupted: InterruptedIOException) {
        throw interrupted
    } catch (_: IOException) {
        return MaterialsLiveRefreshSnapshot(MaterialsLiveRefreshRootState.UNAVAILABLE)
    } catch (_: SecurityException) {
        return MaterialsLiveRefreshSnapshot(MaterialsLiveRefreshRootState.UNAVAILABLE)
    }
    return MaterialsLiveRefreshSnapshot(MaterialsLiveRefreshRootState.DIRECTORY, files, directories)
}

internal fun materialsLiveRefreshChange(
    before: MaterialsLiveRefreshSnapshot,
    after: MaterialsLiveRefreshSnapshot,
    selectedRelativePath: String?,
    selectedTouched: Boolean = false,
): MaterialsLiveRefreshChange {
    val selected = safeMaterialsRefreshRelativePath(selectedRelativePath)
    val beforeFile = selected?.let { before.files[it] }
    val afterFile = selected?.let { after.files[it] }
    return MaterialsLiveRefreshChange(
        // The catalog includes last-modified times for sorting, even when file sizes do not change.
        catalogChanged = before.rootState != after.rootState || before.directories != after.directories ||
            before.files != after.files,
        selectedFileChanged = selected != null &&
            (beforeFile != afterFile || (selectedTouched && (beforeFile != null || afterFile != null))),
    )
}

private fun safeMaterialsRefreshRelativePath(relativePath: String?): String? {
    val portable = relativePath?.replace('\\', '/') ?: return null
    val segments = portable.split('/')
    return portable.takeIf {
        it.isNotEmpty() && !it.startsWith('/') && segments.all { segment ->
            segment.isNotEmpty() && segment != "." && segment != ".." && ':' !in segment &&
                !segment.equals(".git", ignoreCase = true)
        }
    }
}

private fun materialsRefreshEventIsRelevant(root: Path, changed: Path): Boolean {
    if (changed.any { it.toString().equals(".git", ignoreCase = true) || isMaterialsStagingName(it.toString()) }) return false
    return changed == root || changed.startsWith(root) || root.startsWith(changed)
}

private fun materialsPathContainsSymlink(path: Path): Boolean {
    var component = path.root ?: return true
    path.forEach { segment ->
        checkMaterialsRefreshInterrupted()
        component = component.resolve(segment)
        if (Files.isSymbolicLink(component)) return true
    }
    return false
}

private fun materialsWatchAnchor(root: Path): Path? {
    if (root.any { it.toString().equals(".git", ignoreCase = true) }) return null
    var parent = root.parent
    while (parent != null) {
        checkMaterialsRefreshInterrupted()
        if (materialsPathContainsSymlink(parent)) return null
        if (Files.isDirectory(parent, LinkOption.NOFOLLOW_LINKS)) return parent
        parent = parent.parent
    }
    return null
}

private fun checkMaterialsRefreshInterrupted() {
    if (Thread.currentThread().isInterrupted) throw InterruptedIOException("Materials refresh cancelled")
}
