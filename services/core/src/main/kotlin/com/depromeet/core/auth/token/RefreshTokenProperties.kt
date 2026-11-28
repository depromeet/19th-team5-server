package com.depromeet.core.auth.token

import org.springframework.boot.context.properties.ConfigurationProperties
import java.time.Duration

/**
 * @property ttl 수명 14일 (ADR-0004)
 * @property cookieName 리프레시 토큰을 담는 쿠키 이름
 */
@ConfigurationProperties("auth.refresh-token")
data class RefreshTokenProperties(
    val ttl: Duration = Duration.ofDays(14),
    val cookieName: String = "refresh_token",
)
