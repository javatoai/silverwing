package com.snowball.silverwing.core

/**
 * Defines the persistence compatibility boundary for silverwing JSON files.
 *
 * PATCH releases must not change persisted field semantics, so documents from
 * the same MAJOR.MINOR line can be read safely. A MAJOR or MINOR change is a
 * deliberate schema boundary and must be rejected rather than guessed at.
 */
object SchemaVersionCompatibility {
    private val semanticVersion = Regex("""^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$""")

    fun isCompatible(actual: String?, expected: String): Boolean {
        val actualMatch = actual?.let(semanticVersion::matchEntire) ?: return false
        val expectedMatch = semanticVersion.matchEntire(expected) ?: return false
        return actualMatch.groupValues[1] == expectedMatch.groupValues[1] &&
            actualMatch.groupValues[2] == expectedMatch.groupValues[2]
    }
}
