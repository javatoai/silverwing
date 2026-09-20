package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.GroupServiceConfig
import kotlin.test.Test
import kotlin.test.assertEquals

class AddTaskServicesSearchTest {
    private val services = listOf(
        GroupServiceConfig.standard("payment-center", "repo-payment", "支付中心"),
        GroupServiceConfig.standard("mobile-gateway", "repo-mobile", "Mobile Gateway"),
    )

    @Test
    fun `search matches service name and id without case sensitivity`() {
        assertEquals(listOf(services[0]), filterAddTaskServices(services, "支付"))
        assertEquals(listOf(services[1]), filterAddTaskServices(services, "MOBILE-GATEWAY"))
    }

    @Test
    fun `search returns empty list when nothing matches`() {
        assertEquals(emptyList(), filterAddTaskServices(services, "missing-service"))
    }

    @Test
    fun `blank or whitespace query restores all services in original order`() {
        assertEquals(services, filterAddTaskServices(services, "  "))
    }
}
