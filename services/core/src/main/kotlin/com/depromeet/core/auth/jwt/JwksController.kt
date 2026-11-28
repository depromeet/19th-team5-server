package com.depromeet.core.auth.jwt

import org.springframework.http.CacheControl
import org.springframework.http.ResponseEntity
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RestController
import java.util.concurrent.TimeUnit

/**
 * 공개키 목록 (RFC 7517). realtime이 WebSocket 티켓을 검증할 때 받아 간다.
 * realtime은 5분마다 다시 받으므로 캐시도 5분이다.
 */
@RestController
class JwksController(
    private val keys: SigningKeys,
) {
    @GetMapping("/.well-known/jwks.json")
    fun jwks(): ResponseEntity<Map<String, Any>> =
        ResponseEntity
            .ok()
            .cacheControl(CacheControl.maxAge(5, TimeUnit.MINUTES))
            .body(keys.publicJwkSet().toJSONObject())
}
