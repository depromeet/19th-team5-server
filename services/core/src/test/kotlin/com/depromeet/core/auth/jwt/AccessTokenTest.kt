package com.depromeet.core.auth.jwt

import com.nimbusds.jose.JOSEObjectType
import com.nimbusds.jose.JWSAlgorithm
import com.nimbusds.jose.JWSHeader
import com.nimbusds.jose.PlainHeader
import com.nimbusds.jose.crypto.MACSigner
import com.nimbusds.jose.crypto.RSASSASigner
import com.nimbusds.jose.jwk.RSAKey
import com.nimbusds.jwt.JWTClaimsSet
import com.nimbusds.jwt.PlainJWT
import com.nimbusds.jwt.SignedJWT
import org.assertj.core.api.Assertions.assertThat
import org.assertj.core.api.Assertions.assertThatThrownBy
import org.junit.jupiter.api.Test
import org.springframework.security.oauth2.jwt.JwtException
import java.net.URI
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.Date
import java.util.UUID

/**
 * 액세스 토큰 발급과 검증. 검증 순서와 거절 항목은 docs/design/auth.md "토큰 검증"과 ADR-0003을 따른다.
 */
class AccessTokenTest {
    private val properties = JwtProperties(issuer = "https://api.test", audience = "https://api.test")
    private val key = TestKeys.rsa("2026-11-key1")
    private val keys = SigningKeys(listOf(key), signingKid = "2026-11-key1")
    private val config = JwtConfig()
    private val issuer = AccessTokenIssuer(config.jwtEncoder(keys), keys, properties, Clock.systemUTC())
    private val decoder = config.jwtDecoder(keys, properties)

    @Test
    fun `발급한 토큰은 RFC 9068 형식이고 검증을 통과한다`() {
        val token = issuer.issue(memberId = 123, roles = listOf("MEMBER"))

        val header = SignedJWT.parse(token.value).header
        assertThat(header.algorithm).isEqualTo(JWSAlgorithm.RS256)
        assertThat(header.type).isEqualTo(JOSEObjectType("at+jwt"))
        assertThat(header.keyID).isEqualTo("2026-11-key1")

        val jwt = decoder.decode(token.value)
        assertThat(jwt.issuer.toString()).isEqualTo("https://api.test")
        assertThat(jwt.audience).containsExactly("https://api.test")
        assertThat(jwt.subject).isEqualTo("123")
        assertThat(jwt.getClaimAsString("client_id")).isEqualTo("web")
        assertThat(jwt.getClaimAsStringList("roles")).containsExactly("MEMBER")
        assertThat(jwt.id).isNotBlank()
        assertThat(Duration.between(jwt.issuedAt, jwt.expiresAt)).isEqualTo(Duration.ofMinutes(30))
        assertThat(token.expiresAt).isEqualTo(jwt.expiresAt)
    }

    @Test
    fun `이메일 같은 개인정보는 토큰에 넣지 않는다`() {
        val claims = decoder.decode(issuer.issue(memberId = 1, roles = listOf("MEMBER")).value).claims.keys

        assertThat(claims).containsExactlyInAnyOrder("iss", "aud", "sub", "client_id", "roles", "iat", "exp", "jti")
    }

    @Test
    fun `typ이 at+jwt가 아니면 거절한다`() {
        assertRejected(sign(header(type = JOSEObjectType.JWT), claims()))
        assertRejected(sign(header(type = JOSEObjectType("ws-ticket+jwt")), claims()))
        assertRejected(sign(header(type = null), claims()))
    }

    @Test
    fun `iss가 다르면 거절한다`() {
        assertRejected(sign(header(), claims { issuer("https://evil.test") }))
    }

    @Test
    fun `aud에 core가 없으면 거절한다`() {
        assertRejected(sign(header(), claims { audience("wss://rt.test") }))
    }

    @Test
    fun `만료 후 60초까지는 통과하고 그 뒤로는 거절한다`() {
        val now = Instant.now()
        val withinLeeway = sign(header(), claims { issued(now.minusSeconds(1830)).expirationTime(Date.from(now.minusSeconds(30))) })
        val pastLeeway = sign(header(), claims { issued(now.minusSeconds(1890)).expirationTime(Date.from(now.minusSeconds(90))) })

        assertThat(decoder.decode(withinLeeway).subject).isEqualTo("123")
        assertRejected(pastLeeway)
    }

    @Test
    fun `alg none 토큰은 거절한다`() {
        val plain = PlainJWT(PlainHeader.Builder().type(JOSEObjectType("at+jwt")).build(), claims())

        assertRejected(plain.serialize())
    }

    @Test
    fun `공개키를 HMAC 비밀키로 쓴 HS256 토큰은 거절한다 (키 혼동)`() {
        val forged =
            SignedJWT(
                JWSHeader
                    .Builder(JWSAlgorithm.HS256)
                    .type(JOSEObjectType("at+jwt"))
                    .keyID("2026-11-key1")
                    .build(),
                claims(),
            )
        forged.sign(MACSigner(key.toPublicJWK().toRSAPublicKey().encoded))

        assertRejected(forged.serialize())
    }

    @Test
    fun `모르는 kid는 거절한다`() {
        assertRejected(sign(header(kid = "2026-11-key9"), claims(), signer = TestKeys.rsa("2026-11-key9")))
    }

    @Test
    fun `같은 kid라도 다른 키로 서명하면 거절한다`() {
        assertRejected(sign(header(), claims(), signer = TestKeys.rsa("2026-11-key1")))
    }

    @Test
    fun `jku로 공격자 키 주소를 넣어도 따라가지 않는다`() {
        val attacker = TestKeys.rsa("attacker")
        val header =
            JWSHeader
                .Builder(JWSAlgorithm.RS256)
                .type(JOSEObjectType("at+jwt"))
                .keyID("attacker")
                .jwkURL(URI("https://evil.test/jwks.json"))
                .build()

        assertRejected(sign(header, claims(), signer = attacker))
    }

    @Test
    fun `서명에 쓰지 않는 옛 키로 서명된 토큰도 남은 수명 동안 통과한다`() {
        val old = TestKeys.rsa("2026-05-key1")
        val rotated = SigningKeys(listOf(old, key), signingKid = "2026-11-key1")
        val rotatedDecoder = config.jwtDecoder(rotated, properties)

        val oldToken = sign(header(kid = "2026-05-key1"), claims(), signer = old)

        assertThat(rotatedDecoder.decode(oldToken).subject).isEqualTo("123")
    }

    private fun header(
        type: JOSEObjectType? = JOSEObjectType("at+jwt"),
        kid: String = "2026-11-key1",
    ): JWSHeader =
        JWSHeader
            .Builder(JWSAlgorithm.RS256)
            .type(type)
            .keyID(kid)
            .build()

    private fun claims(customize: JWTClaimsSet.Builder.() -> Unit = {}): JWTClaimsSet {
        val now = Instant.now()
        return JWTClaimsSet
            .Builder()
            .issuer("https://api.test")
            .audience("https://api.test")
            .subject("123")
            .claim("client_id", "web")
            .claim("roles", listOf("MEMBER"))
            .issueTime(Date.from(now))
            .expirationTime(Date.from(now.plus(Duration.ofMinutes(30))))
            .jwtID(UUID.randomUUID().toString())
            .apply(customize)
            .build()
    }

    private fun JWTClaimsSet.Builder.issued(at: Instant): JWTClaimsSet.Builder = issueTime(Date.from(at))

    private fun sign(
        header: JWSHeader,
        claims: JWTClaimsSet,
        signer: RSAKey = key,
    ): String = SignedJWT(header, claims).apply { sign(RSASSASigner(signer)) }.serialize()

    private fun assertRejected(token: String) {
        assertThatThrownBy { decoder.decode(token) }.isInstanceOf(JwtException::class.java)
    }
}
