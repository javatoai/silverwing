package com.snowball.silverwing.core

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.time.Duration

data class GenbuTagQueryResult(
    val build: GenbuStageStatus,
    val uat: GenbuStageStatus,
    val production: GenbuStageStatus,
    val notFound: Boolean = false,
    val builtCompletedAt: String? = null,
    val uatReleasedCompletedAt: String? = null,
    val productionReleasedCompletedAt: String? = null,
) {
    init {
        require(
            !notFound ||
                (build == GenbuStageStatus.UNKNOWN && uat == GenbuStageStatus.UNKNOWN && production == GenbuStageStatus.UNKNOWN),
        ) { "未在 Genbu 中找到的 Tag 不应包含发布状态" }
    }
}

fun interface GenbuTagStatusProvider {
    fun query(serviceName: String, tag: String): GenbuTagQueryResult
}

/** Executes the single supported read-only Genbu command for a Tag. */
class ProcessGenbuTagStatusService(
    private val executable: GenbuExecutable = GenbuExecutable.pathFallback(),
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val timeout: Duration = Duration.ofSeconds(20),
    private val json: Json = Json { ignoreUnknownKeys = true },
) : GenbuTagStatusProvider {
    override fun query(serviceName: String, tag: String): GenbuTagQueryResult {
        require(serviceName.isNotBlank()) { "Genbu 服务名不能为空" }
        require(tag.isNotBlank()) { "Tag 不能为空" }
        val result = runner.run(listOf(executable.resolve(), "query-tag", "--json", serviceName, tag), timeout = timeout)
        if (!result.succeeded) {
            val detail = listOf(result.stderr, result.stdout).firstOrNull(String::isNotBlank)?.trim()
            if (detail != null && isGenbuTagNotFound(detail)) {
                return GenbuTagQueryResult(
                    build = GenbuStageStatus.UNKNOWN,
                    uat = GenbuStageStatus.UNKNOWN,
                    production = GenbuStageStatus.UNKNOWN,
                    notFound = true,
                )
            }
            error(detail ?: "genbu query-tag 执行失败（退出码 ${result.exitCode}）")
        }
        return parseGenbuTagQueryJson(result.stdout, json)
    }
}

private fun isGenbuTagNotFound(detail: String): Boolean =
    detail.contains("未在产线", ignoreCase = true) ||
        Regex("""未找到.*(?:Tag|tag)""").containsMatchIn(detail)

@Serializable
private data class GenbuTagQueryJson(
    val service: String? = null,
    val tag: String? = null,
    @SerialName("build_status") val buildStatus: String? = null,
    @SerialName("build_completed_at") val buildCompletedAt: String? = null,
    @SerialName("uat_status") val uatStatus: String? = null,
    @SerialName("uat_completed_at") val uatCompletedAt: String? = null,
    @SerialName("production_status") val productionStatus: String? = null,
    @SerialName("production_completed_at") val productionCompletedAt: String? = null,
)

fun parseGenbuTagQueryJson(output: String, json: Json = Json { ignoreUnknownKeys = true }): GenbuTagQueryResult {
    val parsed = runCatching { json.decodeFromString<GenbuTagQueryJson>(output.trim()) }
        .getOrElse { error("无法识别 Genbu Tag 查询输出") }
    return GenbuTagQueryResult(
        build = genbuStageStatus(parsed.buildStatus),
        uat = genbuStageStatus(parsed.uatStatus),
        production = genbuStageStatus(parsed.productionStatus),
        builtCompletedAt = parsed.buildCompletedAt?.takeIf(String::isNotBlank),
        uatReleasedCompletedAt = parsed.uatCompletedAt?.takeIf(String::isNotBlank),
        productionReleasedCompletedAt = parsed.productionCompletedAt?.takeIf(String::isNotBlank),
    )
}

internal fun genbuStageStatus(value: String?): GenbuStageStatus = when (value?.trim()) {
    "初始" -> GenbuStageStatus.INITIAL
    "构建中" -> GenbuStageStatus.BUILDING
    "成功" -> GenbuStageStatus.SUCCESS
    "失败" -> GenbuStageStatus.FAILED
    else -> GenbuStageStatus.UNKNOWN
}
