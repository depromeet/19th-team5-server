package com.depromeet.core.global.error

import com.depromeet.core.auth.jwt.EphemeralSigningKeySource
import com.depromeet.core.auth.jwt.JwtConfig
import com.depromeet.core.global.security.ProblemSecurityHandler
import com.depromeet.core.global.security.SecurityConfig
import jakarta.validation.Valid
import jakarta.validation.constraints.Email
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.webmvc.test.autoconfigure.WebMvcTest
import org.springframework.context.annotation.Import
import org.springframework.http.MediaType
import org.springframework.security.test.context.support.WithMockUser
import org.springframework.test.web.servlet.MockMvc
import org.springframework.test.web.servlet.get
import org.springframework.test.web.servlet.post
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RestController

@WebMvcTest(controllers = [GlobalExceptionHandlerTest.ErrorTestController::class])
@Import(
    SecurityConfig::class,
    ProblemSecurityHandler::class,
    JwtConfig::class,
    EphemeralSigningKeySource::class,
    GlobalExceptionHandler::class,
    GlobalExceptionHandlerTest.ErrorTestController::class,
)
@WithMockUser
class GlobalExceptionHandlerTest {
    @Autowired
    lateinit var mockMvc: MockMvc

    @Test
    fun `비즈니스 예외는 ErrorCode의 상태와 code로 응답한다`() {
        mockMvc.get("/test/business").andExpect {
            status { isForbidden() }
            content { contentTypeCompatibleWith(MediaType.APPLICATION_PROBLEM_JSON) }
            jsonPath("$.status") { value(403) }
            jsonPath("$.code") { value("FORBIDDEN") }
            jsonPath("$.detail") { value("다른 사람의 예약입니다.") }
        }
    }

    @Test
    fun `잘못된 JSON은 400 INVALID_REQUEST로 응답한다`() {
        mockMvc
            .post("/test/body") {
                contentType = MediaType.APPLICATION_JSON
                content = "{"
            }.andExpect {
                status { isBadRequest() }
                jsonPath("$.code") { value("INVALID_REQUEST") }
            }
    }

    @Test
    fun `검증에 실패하면 400 INVALID_REQUEST와 틀린 칸을 invalidParams로 응답한다`() {
        mockMvc
            .post("/test/validated") {
                contentType = MediaType.APPLICATION_JSON
                content = """{"email":"not-email"}"""
            }.andExpect {
                status { isBadRequest() }
                jsonPath("$.code") { value("INVALID_REQUEST") }
                jsonPath("$.invalidParams[0].name") { value("email") }
                jsonPath("$.invalidParams[0].reason") { exists() }
            }
    }

    @Test
    fun `예상하지 못한 예외는 500 INTERNAL_ERROR로 응답하고 내부 메시지를 숨긴다`() {
        mockMvc.get("/test/unexpected").andExpect {
            status { isInternalServerError() }
            jsonPath("$.code") { value("INTERNAL_ERROR") }
            jsonPath("$.detail") { value(ErrorCode.INTERNAL_ERROR.message) }
        }
    }

    @Test
    fun `없는 경로는 404 NOT_FOUND로 응답한다`() {
        mockMvc.get("/test/nothing").andExpect {
            status { isNotFound() }
            jsonPath("$.code") { value("NOT_FOUND") }
        }
    }

    @RestController
    class ErrorTestController {
        @GetMapping("/test/business")
        fun business(): Unit = throw BusinessException(ErrorCode.FORBIDDEN, "다른 사람의 예약입니다.")

        @PostMapping("/test/body")
        fun body(
            @RequestBody payload: Payload,
        ) {
        }

        @PostMapping("/test/validated")
        fun validated(
            @Valid @RequestBody payload: EmailPayload,
        ) {
        }

        @GetMapping("/test/unexpected")
        fun unexpected(): Unit = throw IllegalStateException("DB 비밀번호가 틀렸습니다")

        data class Payload(
            val value: String,
        )

        data class EmailPayload(
            @field:Email
            val email: String?,
        )
    }
}
