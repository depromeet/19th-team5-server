package com.depromeet.core.auth.jwt

import com.nimbusds.jose.JWSAlgorithm
import com.nimbusds.jose.jwk.KeyUse
import com.nimbusds.jose.jwk.RSAKey
import com.nimbusds.jose.jwk.gen.RSAKeyGenerator

object TestKeys {
    fun rsa(
        kid: String,
        bits: Int = 2048,
    ): RSAKey =
        RSAKeyGenerator(bits, bits < 2048)
            .keyID(kid)
            .algorithm(JWSAlgorithm.RS256)
            .keyUse(KeyUse.SIGNATURE)
            .generate()
}
