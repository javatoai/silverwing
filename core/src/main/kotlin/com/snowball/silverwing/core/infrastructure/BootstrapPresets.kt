package com.snowball.silverwing.core

object BootstrapPresets {
    fun empty(): BootstrapConfig = BootstrapConfig()

    fun codeGraph(): BootstrapConfig = BootstrapConfig(
        commands = listOf(
            BootstrapCommand(
                name = "初始化 CodeGraph 索引",
                executable = "codegraph",
                arguments = listOf("init", "-i"),
                timeoutSeconds = 600,
            ),
        ),
    )

    /** UI example generated from the real schema so documentation cannot drift from decoding. */
    fun example(): BootstrapConfig = BootstrapConfig(
        copyRules = listOf(
            BootstrapCopyRule(source = ".env.example", target = ".env.local", overwrite = false),
        ),
        commands = listOf(
            BootstrapCommand(
                name = "安装前端依赖",
                executable = "pnpm",
                arguments = listOf("install", "--frozen-lockfile"),
                timeoutSeconds = 900,
            ),
        ),
    )
}
