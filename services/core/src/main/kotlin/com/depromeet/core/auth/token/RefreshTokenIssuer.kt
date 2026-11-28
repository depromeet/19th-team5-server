package com.depromeet.core.auth.token

import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.stereotype.Component
import org.springframework.transaction.annotation.Transactional
import java.security.MessageDigest
import java.security.SecureRandom
import java.time.Clock
import java.time.Instant
import java.util.Base64
import java.util.HexFormat
import java.util.UUID

data class IssuedRefreshToken(
    val value: String,
    val expiresAt: Instant,
)

/**
 * 리프레시 토큰은 랜덤 32바이트다. 원문은 쿠키로 주고 DB에는 SHA-256 해시만 둔다 (ADR-0004).
 * 32바이트 랜덤은 추측할 수 없어서 비밀번호처럼 느린 해시가 필요 없다.
 */
@Component
@EnableConfigurationProperties(RefreshTokenProperties::class)
class RefreshTokenIssuer(
    private val familyRepository: RefreshTokenFamilyRepository,
    private val tokenRepository: RefreshTokenRepository,
    private val properties: RefreshTokenProperties,
    private val clock: Clock,
) {
    private val random = SecureRandom()

    /** 로그인·가입 때 새 묶음을 만들고 첫 토큰을 발급한다 */
    @Transactional
    fun issueNewFamily(memberId: Long): IssuedRefreshToken {
        val now = clock.instant()
        val family = familyRepository.save(RefreshTokenFamily(memberId, now))
        return issue(family.id!!, memberId, now)
    }

    private fun issue(
        familyId: UUID,
        memberId: Long,
        now: Instant,
    ): IssuedRefreshToken {
        val bytes = ByteArray(TOKEN_BYTES).also(random::nextBytes)
        val value = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
        val expiresAt = now.plus(properties.ttl)
        tokenRepository.save(RefreshToken(familyId, memberId, hash(value), expiresAt, now))
        return IssuedRefreshToken(value, expiresAt)
    }

    companion object {
        private const val TOKEN_BYTES = 32

        /** 쿠키 값 → DB에 저장·조회하는 값 (SHA-256, 16진수 64자) */
        fun hash(value: String): String =
            HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(value.toByteArray(Charsets.US_ASCII)))
    }
}
