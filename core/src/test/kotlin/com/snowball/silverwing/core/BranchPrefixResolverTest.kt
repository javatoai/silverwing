package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class BranchPrefixResolverTest {
    @Test
    fun `uses Feishu work item id for num placeholder`() {
        assertEquals(
            "feature/zhangsan_7060612727",
            BranchPrefixResolver.resolve(
                "feature/zhangsan_{num}",
                "https://project.feishu.cn/obt/userstory/detail/7060612727",
            ),
        )
    }

    @Test
    fun `ignores query and fragment for ordinary URLs`() {
        assertEquals("feature/42", BranchPrefixResolver.resolve("feature/{num}", "https://example.com/a12/item/42?x=999#888"))
    }

    @Test
    fun `uses final digit run in plain text and trims punctuation`() {
        assertEquals("fix/7060612727", BranchPrefixResolver.resolve("fix/{num}", "需求 12，编号 7060612727。"))
    }

    @Test
    fun `returns null when placeholder cannot be resolved`() {
        assertNull(BranchPrefixResolver.resolve("feature/{num}", "没有编号"))
    }

    @Test
    fun `prefix without placeholder is unchanged`() {
        assertEquals("feature/zhangsan_", BranchPrefixResolver.resolve("feature/zhangsan_", "没有编号"))
    }
}
