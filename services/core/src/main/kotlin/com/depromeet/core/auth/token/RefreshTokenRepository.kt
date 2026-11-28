package com.depromeet.core.auth.token

import org.springframework.data.jpa.repository.JpaRepository
import java.util.UUID

interface RefreshTokenRepository : JpaRepository<RefreshToken, Long> {
    fun findByTokenHash(tokenHash: String): RefreshToken?
}

interface RefreshTokenFamilyRepository : JpaRepository<RefreshTokenFamily, UUID>
