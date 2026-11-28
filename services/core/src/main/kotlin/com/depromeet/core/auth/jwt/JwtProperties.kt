package com.depromeet.core.auth.jwt

import org.springframework.boot.context.properties.ConfigurationProperties
import java.time.Duration

/**
 * 액세스 토큰 설정. 형식은 RFC 9068을 따른다 (ADR-0003).
 *
 * @property issuer `iss`. core 주소
 * @property audience `aud`. 액세스 토큰을 받는 core 주소
 * @property clientId `client_id`. 클라이언트가 웹 하나라 고정값
 * @property accessTokenTtl 수명 30분 (ADR-0001)
 */
@ConfigurationProperties("auth.jwt")
data class JwtProperties(
    val issuer: String,
    val audience: String,
    val clientId: String = "web",
    val accessTokenTtl: Duration = Duration.ofMinutes(30),
)
