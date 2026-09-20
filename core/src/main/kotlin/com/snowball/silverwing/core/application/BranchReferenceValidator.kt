package com.snowball.silverwing.core

fun interface BranchReferenceValidator {
    fun isValid(branch: String): Boolean
}
