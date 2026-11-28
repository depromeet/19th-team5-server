package com.depromeet.core.member

import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.params.ParameterizedTest
import org.junit.jupiter.params.provider.ValueSource

class PasswordPolicyTest {
    @ParameterizedTest
    @ValueSource(strings = ["abcd1234", "abcd1234!", "A1bcdefg"])
    fun `8~64자이고 영문과 숫자가 있으면 통과한다`(password: String) {
        assertThat(PasswordPolicy.isValid(password)).isTrue()
    }

    @ParameterizedTest
    @ValueSource(
        strings = [
            "abc1234", // 7자
            "abcdefgh", // 숫자 없음
            "12345678", // 영문 없음
            "가나다라마바사1", // 영문 없음
        ],
    )
    fun `규칙에 맞지 않으면 거절한다`(password: String) {
        assertThat(PasswordPolicy.isValid(password)).isFalse()
    }

    @ParameterizedTest
    @ValueSource(ints = [64, 65])
    fun `최대 64자까지 받는다`(length: Int) {
        val password = "a1" + "b".repeat(length - 2)
        assertThat(PasswordPolicy.isValid(password)).isEqualTo(length <= 64)
    }

    @ParameterizedTest
    @ValueSource(ints = [23, 24])
    fun `64자 안이어도 UTF-8 72바이트를 넘으면 거절한다`(hangulCount: Int) {
        // 한글 1자 = 3바이트. "a1" 2바이트 + 한글 23자 69바이트 = 71바이트, 24자면 74바이트
        val password = "a1" + "가".repeat(hangulCount)
        assertThat(PasswordPolicy.isValid(password)).isEqualTo(hangulCount == 23)
    }
}
