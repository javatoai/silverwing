package com.snowball.silverwing.desktop

import org.junit.jupiter.api.Test
import kotlin.test.assertTrue

class ControllerInjectionTest {
    @Test
    fun `feature controllers inject services without desktop application dependency`() {
        listOf(
            TaskController::class.java,
            SettingsController::class.java,
            AgentInstructionsController::class.java,
            DeliveryController::class.java,
        ).forEach { controllerClass ->
            val parameterTypes = controllerClass.declaredConstructors
                .flatMap { it.parameterTypes.asList() }
                .toSet()
            assertTrue(
                DesktopApplication::class.java !in parameterTypes,
                "${controllerClass.simpleName} must not depend on DesktopApplication",
            )
        }
    }
}
