package com.snowball.silverwing.core

object TagOutputFormatter {
    fun format(
        requirementLink: String,
        operations: List<TagOperation>,
        includeFailures: Boolean,
    ): String {
        val successes = operations.filter {
            it.state == TagOperationState.SUCCESS && !it.tag.isNullOrBlank()
        }
        val failures = operations.filter { it !in successes }
        val hasFailures = includeFailures && failures.isNotEmpty()
        return buildString {
            val link = requirementLink.trim()
            if (link.isNotEmpty()) {
                append("需求链接：")
                append(link)
            }
            if (hasFailures) {
                if (link.isNotEmpty()) append("\n\n")
                appendTagList(successes)
                append("\n\n构建失败：\n")
                failures.forEachIndexed { index, operation ->
                    if (index > 0) append('\n')
                    append(operation.serviceName)
                    append(" · ")
                    append(operation.state.userFacingLabel())
                    operation.message?.takeIf { it.isNotBlank() }?.let {
                        append(" · ")
                        append(it)
                    }
                }
                append("\n\n测试Tag未全部构建成功，请处理失败项后重试")
            } else {
                if (link.isNotEmpty()) append('\n')
                append("测试Tag已构建完毕，辛苦发版：\n\n")
                appendTagList(successes)
            }
        }
    }

    private fun StringBuilder.appendTagList(operations: List<TagOperation>) {
        if (operations.isEmpty()) {
            append("（无）")
        } else {
            operations.forEachIndexed { index, operation ->
                if (index > 0) append('\n')
                append(operation.serviceName)
                append(" · ")
                append(operation.tag)
            }
        }
    }
}
