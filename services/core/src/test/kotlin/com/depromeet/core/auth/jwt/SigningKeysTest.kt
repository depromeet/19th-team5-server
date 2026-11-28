package com.depromeet.core.auth.jwt

import org.assertj.core.api.Assertions.assertThat
import org.assertj.core.api.Assertions.assertThatThrownBy
import org.junit.jupiter.api.Test

class SigningKeysTest {
    @Test
    fun `서명 키는 signing_kid로 고른다`() {
        val old = TestKeys.rsa("2026-05-key1")
        val new = TestKeys.rsa("2026-11-key1")

        val keys = SigningKeys(listOf(old, new), signingKid = "2026-11-key1")

        assertThat(keys.signing.keyID).isEqualTo("2026-11-key1")
        assertThat(keys.publicJwkSet().keys.map { it.keyID }).containsExactly("2026-05-key1", "2026-11-key1")
    }

    @Test
    fun `2048비트보다 짧은 키는 거부한다`() {
        assertThatThrownBy { SigningKeys(listOf(TestKeys.rsa("weak", bits = 1024)), signingKid = "weak") }
            .isInstanceOf(IllegalArgumentException::class.java)
            .hasMessageContaining("2048")
    }

    @Test
    fun `목록에 없는 signing_kid는 거부한다`() {
        assertThatThrownBy { SigningKeys(listOf(TestKeys.rsa("2026-11-key1")), signingKid = "2026-11-key2") }
            .isInstanceOf(IllegalArgumentException::class.java)
    }

    @Test
    fun `kid가 겹치면 거부한다`() {
        assertThatThrownBy {
            SigningKeys(listOf(TestKeys.rsa("2026-11-key1"), TestKeys.rsa("2026-11-key1")), signingKid = "2026-11-key1")
        }.isInstanceOf(IllegalArgumentException::class.java)
    }

    @Test
    fun `공개 JWKS에는 개인키 값이 없다`() {
        val json = SigningKeys(listOf(TestKeys.rsa("2026-11-key1")), "2026-11-key1").publicJwkSet().toString()

        assertThat(json).contains("\"n\"", "\"e\"", "\"kid\"", "\"RS256\"")
        assertThat(json).doesNotContain("\"d\"", "\"p\"", "\"q\"", "\"dp\"", "\"dq\"", "\"qi\"")
    }
}
