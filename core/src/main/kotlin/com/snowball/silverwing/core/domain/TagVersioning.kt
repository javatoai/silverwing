package com.snowball.silverwing.core

data class VersionTag(
    val name: String,
    val createdAtEpochSeconds: Long,
)

object TagVersioning {
    val validPattern = Regex("""^\d+\.\d+\.\d+(\.\d+|\.beta-\d+)?$""")
    private val betaPattern = Regex("""^(\d+)\.(\d+)\.(\d+)\.beta-(\d+)$""")
    private val numericBuildPattern = Regex("""^(\d+)\.(\d+)\.(\d+)\.(\d+)$""")
    private val releasePattern = Regex("""^(\d+)\.(\d+)\.(\d+)$""")

    fun next(current: String): String {
        require(validPattern.matches(current)) { "不支持的测试Tag格式：$current" }
        betaPattern.matchEntire(current)?.let { match ->
            val (major, minor, patch, beta) = match.destructured
            return "$major.$minor.$patch.beta-${beta.toLong() + 1}"
        }
        numericBuildPattern.matchEntire(current)?.let { match ->
            val (major, minor, patch) = match.destructured
            return "$major.$minor.${patch.toLong() + 1}.beta-1"
        }
        releasePattern.matchEntire(current)?.let { match ->
            val (major, minor, patch) = match.destructured
            return "$major.$minor.${patch.toLong() + 1}.beta-1"
        }
        error("无法计算下一个测试Tag：$current")
    }

    fun latest(tags: List<VersionTag>): String? {
        val parsed = tags.mapNotNull { tag -> parse(tag)?.let { it to tag } }
        if (parsed.isEmpty()) return null

        val latestReleaseLine = parsed
            .filter { (version) -> version.kind != VersionKind.BETA }
            .maxByOrNull { (_, tag) -> tag.createdAtEpochSeconds }
            ?.first
            ?.majorMinor
        val latestBetaLine = parsed
            .filter { (version) -> version.kind == VersionKind.BETA }
            .maxByOrNull { (_, tag) -> tag.createdAtEpochSeconds }
            ?.first
            ?.majorMinor
        val activeLine = when {
            latestReleaseLine == null -> latestBetaLine
            latestBetaLine == null -> latestReleaseLine
            compareValuesBy(
                latestBetaLine,
                latestReleaseLine,
                { it.first },
                { it.second },
            ) > 0 -> latestBetaLine
            else -> latestReleaseLine
        } ?: return null

        return parsed
            .asSequence()
            .filter { (version) -> version.majorMinor == activeLine }
            .maxWithOrNull(
                compareBy<Pair<ParsedVersion, VersionTag>>(
                    { it.first.major },
                    { it.first.minor },
                    { it.first.patch },
                    { it.first.kind.order },
                    { it.first.suffix },
                ),
            )
            ?.second
            ?.name
    }

    private fun parse(tag: VersionTag): ParsedVersion? {
        betaPattern.matchEntire(tag.name)?.let { match ->
            val (major, minor, patch, beta) = match.destructured
            return ParsedVersion(
                major.toLong(),
                minor.toLong(),
                patch.toLong(),
                VersionKind.BETA,
                beta.toLong(),
            )
        }
        numericBuildPattern.matchEntire(tag.name)?.let { match ->
            val (major, minor, patch, build) = match.destructured
            return ParsedVersion(
                major.toLong(),
                minor.toLong(),
                patch.toLong(),
                VersionKind.NUMERIC_BUILD,
                build.toLong(),
            )
        }
        releasePattern.matchEntire(tag.name)?.let { match ->
            val (major, minor, patch) = match.destructured
            return ParsedVersion(
                major.toLong(),
                minor.toLong(),
                patch.toLong(),
                VersionKind.RELEASE,
                0,
            )
        }
        return null
    }

    private enum class VersionKind(val order: Long) {
        BETA(0),
        RELEASE(1),
        NUMERIC_BUILD(2),
    }

    private data class ParsedVersion(
        val major: Long,
        val minor: Long,
        val patch: Long,
        val kind: VersionKind,
        val suffix: Long,
    ) {
        val majorMinor: Pair<Long, Long> = major to minor
    }
}
