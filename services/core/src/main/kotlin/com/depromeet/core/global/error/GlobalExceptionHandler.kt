package com.depromeet.core.global.error

import org.slf4j.LoggerFactory
import org.springframework.http.HttpHeaders
import org.springframework.http.HttpStatusCode
import org.springframework.http.ProblemDetail
import org.springframework.http.ResponseEntity
import org.springframework.web.bind.annotation.ExceptionHandler
import org.springframework.web.bind.annotation.RestControllerAdvice
import org.springframework.web.context.request.WebRequest
import org.springframework.web.servlet.mvc.method.annotation.ResponseEntityExceptionHandler

/**
 * 모든 에러를 Problem JSON + `code`로 응답한다.
 * Spring MVC 기본 예외(잘못된 JSON, 검증 실패 등)는 부모 클래스가 ProblemDetail을 만들고,
 * 여기서 `code`만 붙인다.
 */
@RestControllerAdvice
class GlobalExceptionHandler : ResponseEntityExceptionHandler() {
    private val log = LoggerFactory.getLogger(javaClass)

    @ExceptionHandler(BusinessException::class)
    fun handleBusiness(e: BusinessException): ProblemDetail = ProblemDetails.of(e.errorCode, e.message ?: e.errorCode.message)

    @ExceptionHandler(Exception::class)
    fun handleUnexpected(e: Exception): ProblemDetail {
        log.error("처리하지 못한 예외", e)
        return ProblemDetails.of(ErrorCode.INTERNAL_ERROR, ErrorCode.INTERNAL_ERROR.message)
    }

    // handleExceptionInternal은 body가 null로 들어오고 ProblemDetail은 그 안에서 만들어진다.
    // 응답 직전인 여기서 code를 붙인다.
    override fun createResponseEntity(
        body: Any?,
        headers: HttpHeaders,
        statusCode: HttpStatusCode,
        request: WebRequest,
    ): ResponseEntity<Any> {
        if (body is ProblemDetail && body.properties?.containsKey(ProblemDetails.CODE) != true) {
            body.setProperty(ProblemDetails.CODE, codeFor(statusCode).name)
        }
        return super.createResponseEntity(body, headers, statusCode, request)
    }

    private fun codeFor(status: HttpStatusCode): ErrorCode =
        when (status.value()) {
            400 -> ErrorCode.INVALID_INPUT
            401 -> ErrorCode.UNAUTHORIZED
            403 -> ErrorCode.FORBIDDEN
            404 -> ErrorCode.NOT_FOUND
            else -> if (status.is5xxServerError) ErrorCode.INTERNAL_ERROR else ErrorCode.INVALID_INPUT
        }
}
