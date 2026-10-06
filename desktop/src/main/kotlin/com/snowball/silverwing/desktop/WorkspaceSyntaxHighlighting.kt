package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.WorkspaceFileLanguage
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import java.util.Locale

internal enum class WorkspaceSyntaxTokenKind {
    KEYWORD,
    STRING,
    COMMENT,
    NUMBER,
    ANNOTATION,
    PROPERTY_KEY,
    TAG,
    ATTRIBUTE,
    HEADING,
}

internal data class WorkspaceSyntaxToken(
    val start: Int,
    val end: Int,
    val kind: WorkspaceSyntaxTokenKind,
)

internal fun workspaceHighlightedText(content: String, language: WorkspaceFileLanguage, palette: WorkspacePreviewPalette): AnnotatedString = buildAnnotatedString {
    append(content)
    workspaceDocumentSyntaxTokens(language, content).forEach { token ->
        addStyle(SpanStyle(color = palette.colorFor(token.kind)), token.start, token.end)
    }
}

internal fun syntaxLanguageFromLabel(label: String?): WorkspaceFileLanguage {
    val extension = when (val name = label?.trim()?.substringBefore(' ')?.lowercase(Locale.ROOT)) {
        "python", "python3", "py" -> "py"
        "shell", "shellscript", "bash", "sh", "zsh", "console" -> "sh"
        "powershell", "pwsh", "ps1" -> "ps1"
        "batch", "bat", "cmd" -> "bat"
        "javascript", "js", "jsx" -> "js"
        "typescript", "ts", "tsx" -> "ts"
        "kotlin", "kt", "kts" -> "kt"
        "yml", "yaml" -> "yaml"
        else -> name.orEmpty()
    }
    return WorkspaceFileLanguage.fromPath("code.$extension")
}

/** Lightweight, dependency-free lexical highlighting for workspace text previews. */
internal fun workspaceSyntaxTokens(language: WorkspaceFileLanguage, line: String): List<WorkspaceSyntaxToken> = when (language) {
    WorkspaceFileLanguage.JAVA -> codeTokens(line, javaKeywords, lineComment = "//")
    WorkspaceFileLanguage.KOTLIN -> codeTokens(line, kotlinKeywords, lineComment = "//")
    WorkspaceFileLanguage.GRADLE -> codeTokens(line, gradleKeywords, lineComment = "//")
    WorkspaceFileLanguage.PROPERTIES -> propertiesTokens(line)
    WorkspaceFileLanguage.HTML -> markupTokens(line)
    WorkspaceFileLanguage.CSS -> cssTokens(line)
    WorkspaceFileLanguage.JAVASCRIPT -> codeTokens(line, javaScriptKeywords, lineComment = "//")
    WorkspaceFileLanguage.TYPESCRIPT -> codeTokens(line, typeScriptKeywords, lineComment = "//")
    WorkspaceFileLanguage.JSON -> jsonTokens(line)
    WorkspaceFileLanguage.YAML -> yamlTokens(line)
    WorkspaceFileLanguage.SQL -> codeTokens(line, sqlKeywords, lineComment = "--", caseInsensitiveKeywords = true)
    WorkspaceFileLanguage.PYTHON -> codeTokens(line, pythonKeywords, lineComment = "#", tripleQuotes = true, blockComment = null)
    WorkspaceFileLanguage.SHELL -> codeTokens(line, shellKeywords, lineComment = "#", annotations = false, blockComment = null, variables = true, shellSyntax = true)
    WorkspaceFileLanguage.POWERSHELL -> codeTokens(line, powerShellKeywords, lineComment = "#", caseInsensitiveKeywords = true, blockComment = "<#" to "#>", variables = true, escape = '`', literalSingleQuotes = true)
    WorkspaceFileLanguage.BATCH -> batchTokens(line)
    WorkspaceFileLanguage.MARKDOWN -> markdownTokens(line)
    WorkspaceFileLanguage.PLAIN_TEXT -> emptyList()
}

/** Document-wide scanning keeps multiline strings and comments intact without changing source offsets. */
internal fun workspaceDocumentSyntaxTokens(language: WorkspaceFileLanguage, content: String): List<WorkspaceSyntaxToken> = when (language) {
    WorkspaceFileLanguage.JAVA, WorkspaceFileLanguage.KOTLIN, WorkspaceFileLanguage.GRADLE,
    WorkspaceFileLanguage.JAVASCRIPT, WorkspaceFileLanguage.TYPESCRIPT, WorkspaceFileLanguage.SQL,
    WorkspaceFileLanguage.PYTHON, WorkspaceFileLanguage.SHELL, WorkspaceFileLanguage.POWERSHELL,
    WorkspaceFileLanguage.JSON -> workspaceSyntaxTokens(language, content)
    else -> buildList {
        var offset = 0
        content.splitToSequence('\n').forEach { line ->
            workspaceSyntaxTokens(language, line).forEach { token -> add(token.copy(start = token.start + offset, end = token.end + offset)) }
            offset += line.length + 1
        }
    }
}

private fun codeTokens(
    line: String,
    keywords: Set<String>,
    lineComment: String? = null,
    annotations: Boolean = true,
    caseInsensitiveKeywords: Boolean = false,
    tripleQuotes: Boolean = false,
    blockComment: Pair<String, String>? = "/*" to "*/",
    variables: Boolean = false,
    escape: Char? = '\\',
    shellSyntax: Boolean = false,
    literalSingleQuotes: Boolean = false,
): List<WorkspaceSyntaxToken> {
    val tokens = mutableListOf<WorkspaceSyntaxToken>()
    var index = 0
    while (index < line.length) {
        when {
            lineComment != null && line.startsWith(lineComment, index) && (!shellSyntax || shellCommentStart(line, index)) -> {
                val end = line.indexOf('\n', index).takeIf { it >= 0 } ?: line.length
                tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.COMMENT)
                index = end
            }

            blockComment != null && line.startsWith(blockComment.first, index) -> {
                val end = line.indexOf(blockComment.second, index + blockComment.first.length).let { if (it < 0) line.length else it + blockComment.second.length }
                tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.COMMENT)
                index = end
            }

            line[index] == '\'' || line[index] == '"' -> {
                val end = quotedEnd(line, index, tripleQuotes, if ((shellSyntax || literalSingleQuotes) && line[index] == '\'') null else escape)
                tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.STRING)
                index = end
            }

            shellSyntax && line[index] == '\\' -> index = minOf(index + 2, line.length)

            variables && line[index] == '$' -> {
                val end = when {
                    line.getOrNull(index + 1) == '{' -> line.indexOf('}', index + 2).let { if (it < 0) line.length else it + 1 }
                    line.getOrNull(index + 1)?.let(::isIdentifierStart) == true -> identifierEnd(line, index + 1)
                    line.getOrNull(index + 1)?.let { it.isDigit() || it in "?@*#-$!" } == true -> index + 2
                    else -> index + 1
                }
                tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.PROPERTY_KEY)
                index = end
            }

            annotations && line[index] == '@' && index + 1 < line.length && isIdentifierStart(line[index + 1]) -> {
                val end = identifierEnd(line, index + 1)
                tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.ANNOTATION)
                index = end
            }

            line[index].isDigit() -> {
                val end = numberEnd(line, index)
                tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.NUMBER)
                index = end
            }

            isIdentifierStart(line[index]) -> {
                val end = identifierEnd(line, index)
                val word = line.substring(index, end)
                if ((if (caseInsensitiveKeywords) word.lowercase(Locale.ROOT) else word) in keywords) {
                    tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.KEYWORD)
                }
                index = end
            }

            else -> index++
        }
    }
    return tokens
}

private fun batchTokens(line: String): List<WorkspaceSyntaxToken> {
    val start = line.indexOfFirst { !it.isWhitespace() }.takeIf { it >= 0 } ?: return emptyList()
    val trimmed = line.substring(start).removePrefix("@").lowercase(Locale.ROOT)
    if (trimmed.startsWith("::") || trimmed == "rem" || trimmed.startsWith("rem ") || trimmed.startsWith("rem\t")) {
        return listOf(WorkspaceSyntaxToken(start, line.length, WorkspaceSyntaxTokenKind.COMMENT))
    }
    return normalizeTokens(codeTokens(line, batchKeywords, annotations = false, caseInsensitiveKeywords = true, blockComment = null, escape = null) +
        batchVariables.findAll(line).map { WorkspaceSyntaxToken(it.range.first, it.range.last + 1, WorkspaceSyntaxTokenKind.PROPERTY_KEY) }.toList())
}

private fun propertiesTokens(line: String): List<WorkspaceSyntaxToken> {
    val trimmedStart = line.indexOfFirst { !it.isWhitespace() }.takeIf { it >= 0 } ?: return emptyList()
    if (line.getOrNull(trimmedStart) == '#' || line.getOrNull(trimmedStart) == '!') {
        return listOf(WorkspaceSyntaxToken(trimmedStart, line.length, WorkspaceSyntaxTokenKind.COMMENT))
    }
    val delimiter = propertyDelimiter(line)
    return if (delimiter == null) {
        listOf(WorkspaceSyntaxToken(trimmedStart, line.length, WorkspaceSyntaxTokenKind.PROPERTY_KEY))
    } else {
        buildList {
            if (delimiter > trimmedStart) add(WorkspaceSyntaxToken(trimmedStart, delimiter, WorkspaceSyntaxTokenKind.PROPERTY_KEY))
            val valueStart = (delimiter + 1).let { start ->
                line.indexOfFirstFrom(start) { !it.isWhitespace() }.takeIf { it >= 0 } ?: line.length
            }
            if (valueStart < line.length) add(WorkspaceSyntaxToken(valueStart, line.length, WorkspaceSyntaxTokenKind.STRING))
        }
    }
}

private fun markupTokens(line: String): List<WorkspaceSyntaxToken> {
    val trimmed = line.trimStart()
    if (trimmed.startsWith("<!--")) return listOf(WorkspaceSyntaxToken(line.indexOf("<!--"), line.length, WorkspaceSyntaxTokenKind.COMMENT))
    val tokens = mutableListOf<WorkspaceSyntaxToken>()
    markupTag.findAll(line).forEach { match ->
        tokens += WorkspaceSyntaxToken(match.range.first, match.range.last + 1, WorkspaceSyntaxTokenKind.TAG)
    }
    markupAttribute.findAll(line).forEach { match ->
        tokens += WorkspaceSyntaxToken(match.groups[1]!!.range.first, match.groups[1]!!.range.last + 1, WorkspaceSyntaxTokenKind.ATTRIBUTE)
    }
    quotedStrings.findAll(line).forEach { match ->
        tokens += WorkspaceSyntaxToken(match.range.first, match.range.last + 1, WorkspaceSyntaxTokenKind.STRING)
    }
    return normalizeTokens(tokens)
}

private fun cssTokens(line: String): List<WorkspaceSyntaxToken> {
    if (line.trimStart().startsWith("/*")) return listOf(WorkspaceSyntaxToken(line.indexOf("/*"), line.length, WorkspaceSyntaxTokenKind.COMMENT))
    val tokens = codeTokens(line, cssKeywords, annotations = false, caseInsensitiveKeywords = true).toMutableList()
    cssProperty.find(line)?.let { match ->
        tokens += WorkspaceSyntaxToken(match.groups[1]!!.range.first, match.groups[1]!!.range.last + 1, WorkspaceSyntaxTokenKind.PROPERTY_KEY)
    }
    quotedStrings.findAll(line).forEach { match ->
        tokens += WorkspaceSyntaxToken(match.range.first, match.range.last + 1, WorkspaceSyntaxTokenKind.STRING)
    }
    return normalizeTokens(tokens)
}

private fun jsonTokens(line: String): List<WorkspaceSyntaxToken> {
    val tokens = mutableListOf<WorkspaceSyntaxToken>()
    var index = 0
    while (index < line.length) {
        when {
            line[index] == '"' -> {
                val end = quotedEnd(line, index)
                val next = line.indexOfFirstFrom(end) { !it.isWhitespace() }
                tokens += WorkspaceSyntaxToken(index, end, if (next >= 0 && line[next] == ':') WorkspaceSyntaxTokenKind.PROPERTY_KEY else WorkspaceSyntaxTokenKind.STRING)
                index = end
            }

            line[index].isDigit() || (line[index] == '-' && line.getOrNull(index + 1)?.isDigit() == true) -> {
                val end = numberEnd(line, index)
                tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.NUMBER)
                index = end
            }

            isIdentifierStart(line[index]) -> {
                val end = identifierEnd(line, index)
                if (line.substring(index, end) in jsonKeywords) tokens += WorkspaceSyntaxToken(index, end, WorkspaceSyntaxTokenKind.KEYWORD)
                index = end
            }

            else -> index++
        }
    }
    return tokens
}

private fun yamlTokens(line: String): List<WorkspaceSyntaxToken> {
    val tokens = codeTokens(line, yamlKeywords, lineComment = "#", annotations = false, caseInsensitiveKeywords = true).toMutableList()
    yamlProperty.find(line)?.let { match ->
        tokens += WorkspaceSyntaxToken(match.groups[1]!!.range.first, match.groups[1]!!.range.last + 1, WorkspaceSyntaxTokenKind.PROPERTY_KEY)
    }
    return normalizeTokens(tokens)
}

private fun markdownTokens(line: String): List<WorkspaceSyntaxToken> = when {
    line.trimStart().startsWith("#") -> listOf(WorkspaceSyntaxToken(0, line.length, WorkspaceSyntaxTokenKind.HEADING))
    line.trimStart().startsWith("```") -> listOf(WorkspaceSyntaxToken(0, line.length, WorkspaceSyntaxTokenKind.KEYWORD))
    else -> quotedStrings.findAll(line).map { WorkspaceSyntaxToken(it.range.first, it.range.last + 1, WorkspaceSyntaxTokenKind.STRING) }.toList()
}

private fun normalizeTokens(tokens: List<WorkspaceSyntaxToken>): List<WorkspaceSyntaxToken> {
    val ordered = tokens.sortedWith(compareBy<WorkspaceSyntaxToken> { it.start }.thenByDescending { it.end })
    var previousEnd = 0
    return buildList {
        ordered.forEach { token ->
            if (token.start >= previousEnd && token.end > token.start) {
                add(token)
                previousEnd = token.end
            }
        }
    }
}

private fun quotedEnd(line: String, start: Int, tripleQuotes: Boolean = false, escape: Char? = '\\'): Int {
    val quote = line[start]
    val delimiter = if (tripleQuotes && line.startsWith("$quote$quote$quote", start)) "$quote$quote$quote" else quote.toString()
    var index = start + delimiter.length
    while (index < line.length) {
        if (line[index] == escape) {
            index += 2
        } else if (line.startsWith(delimiter, index)) {
            return index + delimiter.length
        } else {
            index++
        }
    }
    return line.length
}

private fun shellCommentStart(text: String, index: Int): Boolean {
    if (index == 0) return true
    val previous = text[index - 1]
    if (!previous.isWhitespace() && previous !in ";|&()>") return false
    var cursor = index - 2
    var escapes = 0
    while (cursor >= 0 && text[cursor--] == '\\') escapes++
    return escapes % 2 == 0
}

private fun numberEnd(line: String, start: Int): Int {
    var index = start + 1
    while (index < line.length && (line[index].isDigit() || line[index] in ".xXaAbBcCdDeEfF_")) index++
    return index
}

private fun isIdentifierStart(character: Char): Boolean = character == '_' || character == '$' || character.isLetter()

private fun identifierEnd(line: String, start: Int): Int {
    var index = start + 1
    while (index < line.length && (isIdentifierStart(line[index]) || line[index].isDigit())) index++
    return index
}

private fun propertyDelimiter(line: String): Int? {
    var escaped = false
    line.forEachIndexed { index, character ->
        if (!escaped && (character == '=' || character == ':')) return index
        escaped = !escaped && character == '\\'
        if (character != '\\') escaped = false
    }
    return null
}

private fun String.indexOfFirstFrom(start: Int, predicate: (Char) -> Boolean): Int {
    for (index in start until length) if (predicate(this[index])) return index
    return -1
}

private val markupTag = Regex("</?([A-Za-z][A-Za-z0-9:_-]*)")
private val markupAttribute = Regex("\\s([A-Za-z_:][A-Za-z0-9:_.-]*)(?=\\s*=)")
private val quotedStrings = Regex("\"(?:\\\\.|[^\"])*\"|'(?:\\\\.|[^'])*'")
private val cssProperty = Regex("^\\s*([-A-Za-z][A-Za-z0-9-]*)(?=\\s*:)")
private val yamlProperty = Regex("^\\s*(?:-\\s*)?([A-Za-z0-9_.-]+)(?=\\s*:)")
private val batchVariables = Regex("%[^%\\r\\n]+%|%[0-9*]|![^!\\r\\n]+!")

private val javaKeywords = setOf("abstract", "boolean", "break", "byte", "case", "catch", "class", "continue", "default", "do", "double", "else", "enum", "extends", "final", "finally", "float", "for", "if", "implements", "import", "instanceof", "int", "interface", "long", "new", "package", "private", "protected", "public", "return", "short", "static", "strictfp", "super", "switch", "synchronized", "this", "throw", "throws", "transient", "try", "void", "volatile", "while", "true", "false", "null")
private val kotlinKeywords = setOf("as", "break", "class", "continue", "do", "else", "false", "for", "fun", "if", "in", "interface", "is", "null", "object", "package", "return", "super", "this", "throw", "true", "try", "typealias", "val", "var", "when", "while", "by", "catch", "constructor", "delegate", "dynamic", "field", "file", "finally", "get", "import", "init", "param", "property", "receiver", "set", "setparam", "where")
private val gradleKeywords = kotlinKeywords + setOf("plugins", "dependencies", "implementation", "testImplementation", "repositories", "mavenCentral", "version", "group", "tasks")
private val javaScriptKeywords = setOf("async", "await", "break", "case", "catch", "class", "const", "continue", "default", "delete", "do", "else", "export", "extends", "false", "finally", "for", "from", "function", "if", "import", "in", "instanceof", "let", "new", "null", "return", "static", "switch", "this", "throw", "true", "try", "typeof", "undefined", "var", "while", "yield")
private val typeScriptKeywords = javaScriptKeywords + setOf("abstract", "any", "as", "boolean", "declare", "enum", "implements", "interface", "keyof", "namespace", "never", "number", "private", "protected", "public", "readonly", "string", "type", "unknown")
private val cssKeywords = setOf("important", "inherit", "initial", "none", "unset")
private val jsonKeywords = setOf("true", "false", "null")
private val yamlKeywords = setOf("true", "false", "null", "yes", "no", "on", "off")
private val sqlKeywords = setOf("select", "from", "where", "join", "left", "right", "inner", "outer", "on", "insert", "into", "update", "delete", "create", "alter", "drop", "table", "values", "set", "and", "or", "not", "null", "as", "group", "by", "order", "having", "limit", "union", "distinct")
private val shellKeywords = setOf("if", "then", "else", "elif", "fi", "for", "in", "do", "done", "case", "esac", "function", "while", "export", "local", "readonly")
private val pythonKeywords = setOf("False", "None", "True", "and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield", "match", "case")
private val batchKeywords = setOf("echo", "set", "setlocal", "endlocal", "if", "else", "for", "in", "do", "goto", "call", "exit", "pause", "shift", "not", "exist", "defined", "equ", "neq", "lss", "leq", "gtr", "geq")
private val powerShellKeywords = setOf("function", "param", "if", "else", "elseif", "foreach", "for", "while", "switch", "return", "try", "catch", "finally", "throw", "begin", "process", "end", "true", "false", "null")
