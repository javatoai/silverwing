package com.snowball.silverwing.core

class GitWritePolicy(blockedBranches: Collection<String>) {
    private val blocked = blockedBranches.map(String::trim).filter(String::isNotBlank).map(String::lowercase).toSet()

    fun isBlocked(branch: String): Boolean = branch.trim().lowercase() in blocked

    fun requireAllowed(branch: String, operation: String) {
        require(!isBlocked(branch)) { "分支 $branch 已被 Git 写保护，禁止$operation" }
    }
}
