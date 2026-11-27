package com.depromeet.core.global.security

import com.depromeet.core.global.error.ErrorCode
import com.depromeet.core.global.error.ProblemDetails
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.MediaType
import org.springframework.security.access.AccessDeniedException
import org.springframework.security.core.AuthenticationException
import org.springframework.security.oauth2.jwt.JwtValidationException
import org.springframework.security.oauth2.server.resource.web.BearerTokenAuthenticationEntryPoint
import org.springframework.security.oauth2.server.resource.web.access.BearerTokenAccessDeniedHandler
import org.springframework.security.web.AuthenticationEntryPoint
import org.springframework.security.web.access.AccessDeniedHandler
import org.springframework.stereotype.Component
import tools.jackson.databind.json.JsonMapper

/**
 * 보안 필터에서 끝난 401·403도 다른 에러와 같은 Problem JSON + code로 응답한다.
 * WWW-Authenticate 헤더(RFC 6750)는 Spring 기본 처리기가 붙이고, 본문만 여기서 쓴다.
 */
@Component
class ProblemSecurityHandler(
    private val jsonMapper: JsonMapper,
) : AuthenticationEntryPoint,
    AccessDeniedHandler {
    private val bearerEntryPoint = BearerTokenAuthenticationEntryPoint()
    private val bearerDeniedHandler = BearerTokenAccessDeniedHandler()

    override fun commence(
        request: HttpServletRequest,
        response: HttpServletResponse,
        authException: AuthenticationException,
    ) {
        bearerEntryPoint.commence(request, response, authException)
        write(request, response, if (isExpired(authException)) ErrorCode.TOKEN_EXPIRED else ErrorCode.INVALID_TOKEN)
    }

    override fun handle(
        request: HttpServletRequest,
        response: HttpServletResponse,
        accessDeniedException: AccessDeniedException,
    ) {
        bearerDeniedHandler.handle(request, response, accessDeniedException)
        write(request, response, ErrorCode.FORBIDDEN)
    }

    /**
     * 만료만 TOKEN_EXPIRED로 나눈다. 프론트는 이 코드를 받으면 재발급 후 다시 요청한다.
     * 만료 판단은 Spring의 JwtTimestampValidator가 남기는 오류 설명("Jwt expired at ...")으로 한다.
     */
    private fun isExpired(e: Throwable): Boolean =
        generateSequence(e) { it.cause }
            .filterIsInstance<JwtValidationException>()
            .any { ex -> ex.errors.any { it.description?.startsWith("Jwt expired") == true } }

    private fun write(
        request: HttpServletRequest,
        response: HttpServletResponse,
        errorCode: ErrorCode,
    ) {
        val problem = ProblemDetails.of(errorCode, errorCode.message)
        val body =
            mapOf(
                "type" to problem.type.toString(),
                "title" to problem.title,
                "status" to problem.status,
                "detail" to problem.detail,
                "instance" to request.requestURI,
                ProblemDetails.CODE to errorCode.name,
            )
        response.status = errorCode.status.value()
        response.contentType = MediaType.APPLICATION_PROBLEM_JSON_VALUE
        response.characterEncoding = Charsets.UTF_8.name()
        jsonMapper.writeValue(response.outputStream, body)
    }
}
