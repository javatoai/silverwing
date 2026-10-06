import org.jetbrains.compose.desktop.application.dsl.TargetFormat
import org.gradle.api.DefaultTask
import org.gradle.api.GradleException
import org.gradle.api.file.DirectoryProperty
import org.gradle.api.file.RegularFileProperty
import org.gradle.api.provider.ListProperty
import org.gradle.api.provider.Property
import org.gradle.api.tasks.Input
import org.gradle.api.tasks.InputFile
import org.gradle.api.tasks.OutputDirectory
import org.gradle.api.tasks.OutputFile
import org.gradle.api.tasks.Sync
import org.gradle.api.tasks.TaskAction
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.util.Comparator

abstract class WritePortableCliVersion : DefaultTask() {
    @get:Input
    abstract val versionText: Property<String>

    @get:OutputFile
    abstract val destination: RegularFileProperty

    @TaskAction
    fun write() {
        val output = destination.get().asFile.toPath()
        Files.createDirectories(output.parent)
        Files.writeString(output, "${versionText.get()}\n", StandardCharsets.UTF_8)
    }
}

abstract class CreateJlinkRuntime : DefaultTask() {
    @get:InputFile
    abstract val jlinkExecutable: RegularFileProperty

    @get:Input
    abstract val modules: ListProperty<String>

    @get:OutputDirectory
    abstract val destination: DirectoryProperty

    @TaskAction
    fun create() {
        val output = destination.get().asFile.toPath()
        deleteTree(output)
        val process = ProcessBuilder(
            jlinkExecutable.get().asFile.absolutePath,
            "--add-modules", modules.get().joinToString(","),
            "--strip-debug",
            "--no-header-files",
            "--no-man-pages",
            "--compress=zip-6",
            "--output", output.toString(),
        ).redirectErrorStream(true).start()
        val log = process.inputStream.bufferedReader().use { it.readText() }
        if (process.waitFor() != 0) {
            throw GradleException("无法构建 silverwing CLI 运行时：${log.ifBlank { "jlink 退出失败" }}")
        }
    }

    private fun deleteTree(path: java.nio.file.Path) {
        if (!Files.exists(path)) return
        Files.walk(path).use { entries ->
            entries.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
        }
    }
}

val macPackageVersion = version.toString()
val portableCliVersion = layout.buildDirectory.file("generated/silverwing/VERSION")
val portableCliVersionText = version.toString()
val portableCliRuntime = layout.buildDirectory.dir("generated/silverwing-runtime")
val jlinkFileName = if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) "jlink.exe" else "jlink"
val portableCliJlink = javaToolchains.launcherFor {
    languageVersion.set(JavaLanguageVersion.of(21))
}.map { launcher ->
    launcher.metadata.installationPath.file("bin/$jlinkFileName")
}

val writePortableCliVersion = tasks.register<WritePortableCliVersion>("writePortableCliVersion") {
    versionText.set(portableCliVersionText)
    destination.set(portableCliVersion)
}

/** Creates a compact runtime with `java` retained specifically for the CLI. */
val preparePortableCliRuntime = tasks.register<CreateJlinkRuntime>("preparePortableCliRuntime") {
    jlinkExecutable.set(portableCliJlink)
    modules.set(listOf("java.base"))
    destination.set(portableCliRuntime)
}

/**
 * Adds a self-contained CLI payload to the desktop application's resources.
 *
 * The launchers deliberately differ from Gradle's normal `installDist`
 * launchers: in a green package they first use the jpackage runtime that ships
 * alongside the desktop application, so users do not need a separately
 * installed JDK.
 */
val preparePortableCli = tasks.register<Sync>("preparePortableCli") {
    dependsOn(":cli:installDist", writePortableCliVersion, preparePortableCliRuntime)
    from(project(":cli").layout.buildDirectory.dir("install/silverwing")) {
        include("lib/**")
        into("silverwing")
    }
    from(project(":cli").layout.projectDirectory.dir("src/main/portable")) {
        into("silverwing")
    }
    from(portableCliVersion) {
        into("silverwing")
    }
    from(portableCliRuntime) {
        into("silverwing-runtime")
    }
    from(rootProject.layout.projectDirectory.dir("skills/silverwing")) {
        into("skills/silverwing")
    }
    into(layout.buildDirectory.dir("app-resources/common"))
}

plugins {
    kotlin("jvm")
    kotlin("plugin.serialization")
    id("org.jetbrains.compose")
    id("org.jetbrains.kotlin.plugin.compose")
}

kotlin {
    jvmToolchain(21)
}

dependencies {
    implementation(project(":core"))
    implementation(compose.desktop.currentOs)
    implementation(compose.components.resources)
    implementation(compose.material3)
    implementation(compose.materialIconsExtended)
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-swing:1.10.2")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.9.0")
    implementation("io.github.vinceglb:filekit-dialogs:0.14.1")
    implementation("net.java.dev.jna:jna:5.18.1")
    implementation("net.java.dev.jna:jna-platform:5.18.1")
    implementation("com.mikepenz:multiplatform-markdown-renderer:0.43.0")
    implementation("com.mikepenz:multiplatform-markdown-renderer-m3:0.43.0")
    implementation("com.mikepenz:multiplatform-markdown-renderer-coil3:0.43.0")
    implementation("org.apache.pdfbox:pdfbox:3.0.8")
    testImplementation(kotlin("test"))
    testImplementation("org.junit.jupiter:junit-jupiter:5.13.4")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.10.2")
}

compose.resources {
    publicResClass = true
    packageOfResClass = "com.snowball.silverwing.desktop.generated.resources"
}

compose.desktop {
    application {
        mainClass = "com.snowball.silverwing.desktop.MainKt"

        nativeDistributions {
            appResourcesRootDir.set(layout.buildDirectory.dir("app-resources"))
            targetFormats(TargetFormat.Msi, TargetFormat.Exe, TargetFormat.Dmg)
            packageName = "silverwing"
            // Keep native package metadata aligned with the Gradle product version.
            packageVersion = macPackageVersion
            description = "Task-level Agent development workspace orchestrator"
            vendor = "Snowball Technology"

            windows {
                iconFile.set(project.file("src/main/resources/app-icon.ico"))
                menuGroup = "silverwing"
                shortcut = true
                perUserInstall = true
                upgradeUuid = "03e01129-8c93-438e-81d4-fd7e6b76259e"
            }

            macOS {
                iconFile.set(project.file("src/main/resources/app-icon.icns"))
                bundleID = "com.snowball.silverwing"
                dockName = "silverwing"
            }
        }
    }
}

tasks.matching { it.name == "prepareAppResources" }.configureEach {
    dependsOn(preparePortableCli)
}
