package com.depromeet.core.auth.jwt

import com.nimbusds.jose.jwk.JWKSet
import com.nimbusds.jose.jwk.source.ImmutableJWKSet
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.security.oauth2.jose.jws.SignatureAlgorithm
import org.springframework.security.oauth2.jwt.JwtDecoder
import org.springframework.security.oauth2.jwt.JwtEncoder
import org.springframework.security.oauth2.jwt.JwtValidators
import org.springframework.security.oauth2.jwt.NimbusJwtDecoder
import org.springframework.security.oauth2.jwt.NimbusJwtEncoder

@Configuration
@EnableConfigurationProperties(JwtProperties::class)
class JwtConfig {
    @Bean
    fun signingKeys(source: SigningKeySource): SigningKeys = source.load()

    @Bean
    fun jwtEncoder(keys: SigningKeys): JwtEncoder = NimbusJwtEncoder(ImmutableJWKSet(JWKSet(keys.all)))

    /**
     * 메모리의 공개키로만 검증한다. 자기 JWKS를 HTTP로 다시 읽지 않고, 헤더의 jku·x5u도 따르지 않는다.
     * alg는 헤더를 믿지 않고 RS256으로 고정한다 (RFC 8725).
     * 검증 항목은 RFC 9068: typ(at+jwt), iss, aud, client_id, exp(시계 여유 60초), 필수 클레임.
     */
    @Bean
    fun jwtDecoder(
        keys: SigningKeys,
        properties: JwtProperties,
    ): JwtDecoder =
        NimbusJwtDecoder
            .withJwkSource(ImmutableJWKSet(keys.publicJwkSet()))
            .jwsAlgorithm(SignatureAlgorithm.RS256)
            .build()
            .apply {
                setJwtValidator(
                    JwtValidators
                        .createAtJwtValidator()
                        .issuer(properties.issuer)
                        .audience(properties.audience)
                        .clientId(properties.clientId)
                        .build(),
                )
            }
}
