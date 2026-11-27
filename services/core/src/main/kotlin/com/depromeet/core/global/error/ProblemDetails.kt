package com.depromeet.core.global.error

import org.springframework.http.HttpStatusCode
import org.springframework.http.ProblemDetail

/**
 * Problem JSON(RFC 9457)에 `code`를 붙인다.
 */
object ProblemDetails {
    const val CODE = "code"

    fun of(
        errorCode: ErrorCode,
        detail: String,
        status: HttpStatusCode = errorCode.status,
    ): ProblemDetail =
        ProblemDetail.forStatusAndDetail(status, detail).apply {
            setProperty(CODE, errorCode.name)
        }
}
