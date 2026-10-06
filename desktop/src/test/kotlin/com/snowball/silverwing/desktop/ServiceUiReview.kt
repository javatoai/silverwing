package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.*
import com.snowball.silverwing.core.*
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.nio.file.Files

/** 手工回归入口，所有保存和远程地址均使用独立临时数据，不触碰用户配置或 Git remote。 */
fun main() = application {
    val paths = remember { ApplicationPaths(Files.createTempDirectory("silverwing-ui-review")) }
    val store = remember { ConfigStore(paths) }
    val repository = remember { RepositoryConfig("repo", "Repo", paths.home.resolve("repo").toString(), paths.home.resolve("repo/.git").toString(), "https://example.invalid/repo.git") }
    val initial = remember { GroupServiceConfig.standard("service", "repo", "silverwing") }
    val addresses = remember { listOf(
        RepositoryRemoteAddress("origin", "https://github.com/org/repo.git", true, true),
        RepositoryRemoteAddress("github", "github.com:org/nested/repo.git", true, false),
        RepositoryRemoteAddress("github", "ssh://github.com:2222/org/write.git", false, true),
        RepositoryRemoteAddress("upstream", "https://gitlab.example.invalid/" + "nested/".repeat(25) + "repository.git", true, true),
        RepositoryRemoteAddress("local", "C:/repositories/silverwing.git", true, true),
    ) }
    val controller = remember {
        store.save(AppConfig(repositories = listOf(repository), groups = listOf(GroupConfig("group", "Group", services = listOf(initial)))))
        DesktopApplication(paths = paths, configStore = store,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) },
            remoteBranchCatalog = object : RemoteBranchCatalog { override fun list(repository: java.nio.file.Path, remote: String) = listOf(remote + "/master", remote + "/release/test") },
            repositoryRemoteCatalog = RepositoryRemoteCatalog { listOf("origin", "upstream") },
            repositoryRemoteAddressCatalog = RepositoryRemoteAddressCatalog { addresses })
    }
    var service by remember { mutableStateOf(initial) }
    var editing by remember { mutableStateOf(false) }
    var dark by remember { mutableStateOf(false) }
    var failSave by remember { mutableStateOf(true) }
    val scope = rememberCoroutineScope()
    val state = rememberWindowState(width = 1200.dp, height = 820.dp)
    Window(onCloseRequest = { controller.close(); exitApplication() }, title = "silverwing 服务界面检查", state = state) {
        SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
            Surface(Modifier.fillMaxSize()) {
                Column(Modifier.padding(20.dp).verticalScroll(rememberScrollState())) {
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        Button(onClick = { dark = !dark }) { Text("切换深浅主题") }
                        Button(onClick = { state.size = if (state.size.width > 800.dp) androidx.compose.ui.unit.DpSize(740.dp, 620.dp) else androidx.compose.ui.unit.DpSize(1200.dp, 820.dp) }) { Text("切换窄窗口") }
                        Button(onClick = { failSave = !failSave }) { Text(if (failSave) "保存会失败" else "保存会成功") }
                    }
                    ServiceListRow(service, repository, false, false, false, true,
                        { editing = true }, {}, {}, {})
                }
                if (editing) ServiceEditorDialog(controller, service, { editing = false }) { draft, onFailure ->
                    scope.launch { delay(8000); if (failSave) onFailure(IllegalStateException("模拟磁盘写入失败")) else { service = draft; editing = false } }
                    true
                }
            }
        }
    }
}
