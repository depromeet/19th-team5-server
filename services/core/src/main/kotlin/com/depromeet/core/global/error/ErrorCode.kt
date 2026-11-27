package com.depromeet.core.global.error

import org.springframework.http.HttpStatus

/**
 * 응답의 `code` 값. 프론트는 HTTP 상태보다 이 값을 보고 분기한다.
 * 새 코드는 API 명세(노션)에 먼저 추가하고 여기에 옮긴다.
 */
enum class ErrorCode(
    val status: HttpStatus,
    val message: String,
) {
    INVALID_INPUT(HttpStatus.BAD_REQUEST, "요청 값이 올바르지 않습니다."),
    UNAUTHORIZED(HttpStatus.UNAUTHORIZED, "인증이 필요합니다."),
    FORBIDDEN(HttpStatus.FORBIDDEN, "권한이 없습니다."),
    NOT_FOUND(HttpStatus.NOT_FOUND, "찾을 수 없습니다."),
    INTERNAL_ERROR(HttpStatus.INTERNAL_SERVER_ERROR, "서버 오류가 발생했습니다."),
}
