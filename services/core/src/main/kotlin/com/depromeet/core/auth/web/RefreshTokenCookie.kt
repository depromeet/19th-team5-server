package com.depromeet.core.auth.web

import com.depromeet.core.auth.token.IssuedRefreshToken
import com.depromeet.core.auth.token.RefreshTokenProperties
import org.springframework.http.ResponseCookie
import org.springframework.stereotype.Component

/**
 * 리프레시 쿠키. JS가 읽지 못하고(HttpOnly), HTTPS로만 가고(Secure),
 * 다른 사이트에서 보낸 요청에는 실리지 않고(SameSite=Strict), 인증 API에만 실린다(Path).
 * localhost는 브라우저가 안전한 출처로 봐서 HTTP여도 Secure 쿠키를 받는다.
 */
@Component
class RefreshTokenCookie(
    private val properties: RefreshTokenProperties,
) {
    fun create(token: IssuedRefreshToken): ResponseCookie =
        ResponseCookie
            .from(properties.cookieName, token.value)
            .httpOnly(true)
            .secure(true)
            .sameSite("Strict")
            .path(PATH)
            .maxAge(properties.ttl)
            .build()

    companion object {
        const val PATH = "/api/v1/auth"
    }
}
