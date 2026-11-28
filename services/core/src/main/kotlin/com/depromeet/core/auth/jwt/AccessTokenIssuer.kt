package com.depromeet.core.auth.jwt

import org.springframework.security.oauth2.jose.jws.SignatureAlgorithm
import org.springframework.security.oauth2.jwt.JwsHeader
import org.springframework.security.oauth2.jwt.JwtClaimsSet
import org.springframework.security.oauth2.jwt.JwtEncoder
import org.springframework.security.oauth2.jwt.JwtEncoderParameters
import org.springframework.stereotype.Component
import java.time.Clock
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID

data class AccessToken(
    val value: String,
    val expiresAt: Instant,
)

/**
 * 액세스 토큰을 발급한다. 개인정보(이메일, 이름)는 넣지 않는다.
 */
@Component
class AccessTokenIssuer(
    private val encoder: JwtEncoder,
    private val keys: SigningKeys,
    private val properties: JwtProperties,
    private val clock: Clock,
) {
    fun issue(
        memberId: Long,
        roles: Collection<String>,
    ): AccessToken {
        // JWT 시각은 초 단위라 응답의 만료 시각도 초로 맞춘다
        val now = clock.instant().truncatedTo(ChronoUnit.SECONDS)
        val expiresAt = now.plus(properties.accessTokenTtl)
        val header =
            JwsHeader
                .with(SignatureAlgorithm.RS256)
                .type(TYPE)
                .keyId(keys.signing.keyID)
                .build()
        val claims =
            JwtClaimsSet
                .builder()
                .issuer(properties.issuer)
                .audience(listOf(properties.audience))
                .subject(memberId.toString())
                .claim("client_id", properties.clientId)
                .claim("roles", roles.toList())
                .issuedAt(now)
                .expiresAt(expiresAt)
                .id(UUID.randomUUID().toString())
                .build()
        val value = encoder.encode(JwtEncoderParameters.from(header, claims)).tokenValue
        return AccessToken(value, expiresAt)
    }

    companion object {
        const val TYPE = "at+jwt"
    }
}
