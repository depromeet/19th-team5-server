package com.depromeet.core.global.error

import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.Test

/**
 * 에러 코드가 노션 API 명세와 같은지 확인한다. 코드를 바꾸면 명세도 같이 바꿔야 이 테스트가 통과한다.
 * 명세: API 명세서 > 공통 에러 코드, 인증/인가 API 명세 > 에러 코드
 */
class ErrorCodeSpecTest {
    private val spec =
        mapOf(
            "INVALID_REQUEST" to 400,
            "INVALID_PASSWORD_FORMAT" to 400,
            "INVALID_TOKEN" to 401,
            "TOKEN_EXPIRED" to 401,
            "FORBIDDEN" to 403,
            // 명세 공통 표에 추가를 제안한 코드
            "NOT_FOUND" to 404,
            "INTERNAL_ERROR" to 500,
            // 인증
            "INVALID_CREDENTIALS" to 401,
            "DUPLICATE_EMAIL" to 409,
        )

    @Test
    fun `에러 코드 이름과 상태가 명세와 같다`() {
        assertThat(ErrorCode.entries.associate { it.name to it.status.value() }).isEqualTo(spec)
    }
}
