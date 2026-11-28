package com.depromeet.core.global.error

import org.springframework.http.HttpStatus

/**
 * 응답의 `code` 값. 프론트는 HTTP 상태가 아니라 이 값으로 분기한다.
 * 기준은 노션 API 명세(공통 규칙의 "공통 에러 코드"와 도메인별 에러 코드)다. 명세에 먼저 추가하고 여기에 옮긴다.
 * NOT_FOUND, INTERNAL_ERROR는 명세 공통 표에 추가를 제안한 코드다.
 */
enum class ErrorCode(
    val status: HttpStatus,
    val message: String,
) {
    // 공통
    INVALID_REQUEST(HttpStatus.BAD_REQUEST, "요청 값이 올바르지 않습니다."),
    INVALID_PASSWORD_FORMAT(HttpStatus.BAD_REQUEST, "비밀번호는 8~64자이고 영문과 숫자를 포함해야 합니다."),
    INVALID_TOKEN(HttpStatus.UNAUTHORIZED, "액세스 토큰이 없거나 올바르지 않습니다."),
    TOKEN_EXPIRED(HttpStatus.UNAUTHORIZED, "액세스 토큰이 만료되었습니다."),
    FORBIDDEN(HttpStatus.FORBIDDEN, "권한이 없습니다."),
    NOT_FOUND(HttpStatus.NOT_FOUND, "찾을 수 없습니다."),
    INTERNAL_ERROR(HttpStatus.INTERNAL_SERVER_ERROR, "서버 오류가 발생했습니다."),

    // 인증
    INVALID_CREDENTIALS(HttpStatus.UNAUTHORIZED, "이메일 또는 비밀번호가 올바르지 않습니다."),
    DUPLICATE_EMAIL(HttpStatus.CONFLICT, "이미 가입된 이메일입니다."),
}
