package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.LocalGitEnvironmentSnapshot
import com.snowball.silverwing.core.GitConfigValue
import kotlin.test.Test
import kotlin.test.assertEquals

class LocalGitSettingsPresentationTest {
    @Test
    fun `refresh state retains the last successful git summary`() {
        val snapshot = LocalGitEnvironmentSnapshot(
            gitExecutable = "git",
            gitVersion = "git version test",
            systemUser = "tester",
            globalUserName = null,
            globalUserEmail = null,
            globalCredentialHelpers = emptyList(),
            globalKeyConfig = emptyList(),
            errors = emptyList(),
        )

        assertEquals(snapshot, displayedGitSnapshot(LocalGitSettingsState.Loading(snapshot)))
        assertEquals(null, displayedGitSnapshot(LocalGitSettingsState.Loading()))
    }

    @Test
    fun `structured git summary does not repeat identity configuration`() {
        val visible = visibleGlobalGitKeyConfig(
            LocalGitEnvironmentSnapshot(
                gitExecutable = "git",
                gitVersion = "git version test",
                systemUser = "tester",
                globalUserName = GitConfigValue("user.name", "Tester"),
                globalUserEmail = GitConfigValue("user.email", "tester@example.com"),
                globalCredentialHelpers = emptyList(),
                globalKeyConfig = listOf(
                    GitConfigValue("user.name", "Tester"),
                    GitConfigValue("user.email", "tester@example.com"),
                    GitConfigValue("core.autocrlf", "input"),
                ),
                errors = emptyList(),
            ),
        )

        assertEquals(listOf(GitConfigValue("core.autocrlf", "input")), visible)
    }

    @Test
    fun `git settings display only executable user and global configuration`() {
        val text = formatLocalGitSettings(
            LocalGitEnvironmentSnapshot(
                gitExecutable = "C:\\Program Files\\Git\\cmd\\git.exe",
                gitVersion = "git version 2.47.0.windows.2",
                systemUser = "wangzhen",
                globalUserName = GitConfigValue("user.name", "王震", "file:C:/Users/16776/.gitconfig"),
                globalUserEmail = GitConfigValue("user.email", "zhen.wang@snowballtech.com", "file:C:/Users/16776/.gitconfig"),
                globalCredentialHelpers = emptyList(),
                globalKeyConfig = listOf(
                    GitConfigValue("user.name", "王震", "file:C:/Users/16776/.gitconfig"),
                    GitConfigValue("user.email", "zhen.wang@snowballtech.com", "file:C:/Users/16776/.gitconfig"),
                    GitConfigValue("core.autocrlf", "input", "file:C:/Users/16776/.gitconfig"),
                ),
                errors = emptyList(),
            ),
        )

        assertEquals(
            """
            Git 可执行文件：C:\Program Files\Git\cmd\git.exe
            Git 版本：git version 2.47.0.windows.2
            系统用户：wangzhen
            全局 user.name：王震  [file:C:/Users/16776/.gitconfig]
            全局 user.email：zhen.wang@snowballtech.com  [file:C:/Users/16776/.gitconfig]
            全局 credential.helper：未配置
            全局关键配置：
              user.name=王震  [file:C:/Users/16776/.gitconfig]
              user.email=zhen.wang@snowballtech.com  [file:C:/Users/16776/.gitconfig]
              core.autocrlf=input  [file:C:/Users/16776/.gitconfig]
            """.trimIndent(),
            text,
        )
    }
}
