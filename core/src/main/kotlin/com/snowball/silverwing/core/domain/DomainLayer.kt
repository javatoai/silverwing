@file:Suppress("unused")

package com.snowball.silverwing.core

/**
 * Domain layer marker. Files in this directory contain persisted models and pure policies only;
 * they must not invoke Git, JSON storage, processes, or desktop APIs.
 */
internal object DomainLayer
