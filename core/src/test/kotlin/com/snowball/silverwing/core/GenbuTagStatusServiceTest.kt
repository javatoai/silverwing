package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class GenbuTagStatusServiceTest {
    @Test
    fun `query invokes Genbu JSON output with the configured service and tag`() {
        val commands = mutableListOf<List<String>>()
        val provider = ProcessGenbuTagStatusService(
            executable = GenbuExecutable { "D:/cli/genbu.exe" },
            runner = object : CommandRunner {
                override fun run(command: List<String>, workingDirectory: java.nio.file.Path?, timeout: java.time.Duration, environment: Map<String, String>): CommandResult {
                    commands += command
                    return CommandResult(
                        0,
                        """
                        {
                          "service": "fp-payment-center",
                          "tag": "1.2.3.4",
                          "build_status": "成功",
                          "build_completed_at": "2026-08-26 10:00:00",
                          "uat_status": "初始",
                          "uat_completed_at": null,
                          "production_status": "初始",
                          "production_completed_at": null
                        }
                        """.trimIndent(),
                        "",
                    )
                }
            },
        )

        assertEquals(
            GenbuTagQueryResult(
                build = GenbuStageStatus.SUCCESS,
                uat = GenbuStageStatus.INITIAL,
                production = GenbuStageStatus.INITIAL,
                builtCompletedAt = "2026-08-26 10:00:00",
            ),
            provider.query("payment-center", "1.2.3.4"),
        )
        assertEquals(listOf("D:/cli/genbu.exe", "query-tag", "--json", "payment-center", "1.2.3.4"), commands.single())
    }

    @Test
    fun `parser maps every pipeline stage status`() {
        assertEquals(GenbuStageStatus.INITIAL, genbuStageStatus("初始"))
        assertEquals(GenbuStageStatus.BUILDING, genbuStageStatus("构建中"))
        assertEquals(GenbuStageStatus.SUCCESS, genbuStageStatus("成功"))
        assertEquals(GenbuStageStatus.FAILED, genbuStageStatus("失败"))
        assertEquals(GenbuStageStatus.UNKNOWN, genbuStageStatus("未知"))
        assertEquals(GenbuStageStatus.UNKNOWN, genbuStageStatus(null))
        assertEquals(GenbuStageStatus.UNKNOWN, genbuStageStatus(""))
    }

    @Test
    fun `parser keeps failed build and failed release stages distinct`() {
        assertEquals(
            GenbuTagQueryResult(
                build = GenbuStageStatus.FAILED,
                uat = GenbuStageStatus.INITIAL,
                production = GenbuStageStatus.INITIAL,
            ),
            parseGenbuTagQueryJson(
                """
                {
                  "service": "bp-operation-center",
                  "tag": "1.6.92.beta-2",
                  "build_status": "失败",
                  "build_completed_at": null,
                  "uat_status": "初始",
                  "uat_completed_at": null,
                  "production_status": "初始",
                  "production_completed_at": null
                }
                """.trimIndent(),
            ),
        )
        assertEquals(
            GenbuTagQueryResult(
                build = GenbuStageStatus.SUCCESS,
                uat = GenbuStageStatus.FAILED,
                production = GenbuStageStatus.INITIAL,
                builtCompletedAt = "2026-08-26 10:42:28",
                uatReleasedCompletedAt = null,
            ),
            parseGenbuTagQueryJson(
                """
                {
                  "service": "bp-operation-center",
                  "tag": "1.6.92.beta-2",
                  "build_status": "成功",
                  "build_completed_at": "2026-08-26 10:42:28",
                  "uat_status": "失败",
                  "uat_completed_at": null,
                  "production_status": "初始",
                  "production_completed_at": null
                }
                """.trimIndent(),
            ),
        )
    }

    @Test
    fun `parser reads building and released stages with completion times`() {
        assertEquals(
            GenbuTagQueryResult(
                build = GenbuStageStatus.BUILDING,
                uat = GenbuStageStatus.INITIAL,
                production = GenbuStageStatus.INITIAL,
            ),
            parseGenbuTagQueryJson(
                """{"service":"s","tag":"1.0.0","build_status":"构建中","build_completed_at":null,"uat_status":"初始","uat_completed_at":null,"production_status":"初始","production_completed_at":null}""",
            ),
        )
        assertEquals(
            GenbuTagQueryResult(
                build = GenbuStageStatus.SUCCESS,
                uat = GenbuStageStatus.SUCCESS,
                production = GenbuStageStatus.SUCCESS,
                builtCompletedAt = "2026-08-26 10:42:28",
                uatReleasedCompletedAt = "2026-08-26 11:42:28",
                productionReleasedCompletedAt = "2026-08-26 12:42:28",
            ),
            parseGenbuTagQueryJson(
                """
                {
                  "service": "bp-operation-center",
                  "tag": "1.6.92.beta-2",
                  "build_status": "成功",
                  "build_completed_at": "2026-08-26 10:42:28",
                  "uat_status": "成功",
                  "uat_completed_at": "2026-08-26 11:42:28",
                  "production_status": "成功",
                  "production_completed_at": "2026-08-26 12:42:28"
                }
                """.trimIndent(),
            ),
        )
    }

    @Test
    fun `parser rejects non JSON output`() {
        assertFailsWith<IllegalStateException> { parseGenbuTagQueryJson("Tag 构建完成: true\nUAT 发版完成: false") }
    }

    @Test
    fun `query maps Genbu Tag lookup misses to a stable status`() {
        val provider = ProcessGenbuTagStatusService(
            executable = GenbuExecutable { "genbu.exe" },
            runner = object : CommandRunner {
                override fun run(command: List<String>, workingDirectory: java.nio.file.Path?, timeout: java.time.Duration, environment: Map<String, String>) =
                    CommandResult(1, "", "错误：未找到服务 bp-operation-center 的 Tag 1.6.92.beta-2")
            },
        )

        assertEquals(
            GenbuTagQueryResult(
                build = GenbuStageStatus.UNKNOWN,
                uat = GenbuStageStatus.UNKNOWN,
                production = GenbuStageStatus.UNKNOWN,
                notFound = true,
            ),
            provider.query("operation-center", "1.6.92.beta-2"),
        )
    }

}
