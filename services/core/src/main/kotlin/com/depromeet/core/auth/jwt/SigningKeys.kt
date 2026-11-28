package com.depromeet.core.auth.jwt

import com.nimbusds.jose.JWSAlgorithm
import com.nimbusds.jose.jwk.JWKSet
import com.nimbusds.jose.jwk.KeyUse
import com.nimbusds.jose.jwk.RSAKey

/**
 * 서명 키 목록과 지금 서명에 쓸 키 (ADR-0010).
 * 목록의 공개키는 모두 검증과 JWKS에 쓰고, 서명은 [signingKid] 키로만 한다.
 * 교체 중에는 옛 키와 새 키가 함께 들어 있다.
 */
class SigningKeys(
    keys: List<RSAKey>,
    signingKid: String,
) {
    val all: List<RSAKey>
    val signing: RSAKey

    init {
        require(keys.isNotEmpty()) { "서명 키가 하나도 없습니다." }
        val kids = keys.map { it.keyID }
        require(kids.none { it.isNullOrBlank() }) { "kid가 없는 키가 있습니다." }
        require(kids.toSet().size == kids.size) { "kid가 겹칩니다: $kids" }
        keys.forEach { key ->
            require(key.isPrivate) { "개인키가 아닙니다: ${key.keyID}" }
            require(key.size() >= MIN_RSA_BITS) { "RSA 키는 ${MIN_RSA_BITS}비트 이상이어야 합니다: ${key.keyID} (${key.size()}비트)" }
        }
        all =
            keys.map {
                RSAKey
                    .Builder(it)
                    .algorithm(JWSAlgorithm.RS256)
                    .keyUse(KeyUse.SIGNATURE)
                    .build()
            }
        signing = all.find { it.keyID == signingKid } ?: throw IllegalArgumentException("목록에 없는 signing_kid입니다: $signingKid")
    }

    /** 검증과 JWKS 공개용. 개인키 값(d, p, q 등)을 뺀다. */
    fun publicJwkSet(): JWKSet = JWKSet(all.map { it.toPublicJWK() })

    companion object {
        /** RFC 7518 3.3 */
        const val MIN_RSA_BITS = 2048
    }
}
