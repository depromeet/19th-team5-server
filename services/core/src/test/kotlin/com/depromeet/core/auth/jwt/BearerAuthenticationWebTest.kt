package com.depromeet.core.auth.jwt

import com.depromeet.core.global.config.ClockConfig
import com.depromeet.core.global.security.ProblemSecurityHandler
import com.depromeet.core.global.security.SecurityConfig
import org.hamcrest.Matchers.containsString
import org.hamcrest.Matchers.not
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.webmvc.test.autoconfigure.WebMvcTest
import org.springframework.context.annotation.Import
import org.springframework.http.HttpHeaders
import org.springframework.security.core.annotation.AuthenticationPrincipal
import org.springframework.security.core.context.SecurityContextHolder
import org.springframework.security.oauth2.jwt.Jwt
import org.springframework.test.web.servlet.MockMvc
import org.springframework.test.web.servlet.get
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RestController

@WebMvcTest(controllers = [JwksController::class, BearerAuthenticationWebTest.MeController::class])
@Import(
    SecurityConfig::class,
    ProblemSecurityHandler::class,
    JwtConfig::class,
    EphemeralSigningKeySource::class,
    AccessTokenIssuer::class,
    ClockConfig::class,
    BearerAuthenticationWebTest.MeController::class,
)
class BearerAuthenticationWebTest {
    @Autowired
    lateinit var mockMvc: MockMvc

    @Autowired
    lateinit var issuer: AccessTokenIssuer

    @Test
    fun `토큰이 없으면 401 UNAUTHORIZED와 WWW-Authenticate Bearer로 응답한다`() {
        mockMvc.get("/test/me").andExpect {
            status { isUnauthorized() }
            header { string(HttpHeaders.WWW_AUTHENTICATE, containsString("Bearer")) }
            jsonPath("$.status") { value(401) }
            jsonPath("$.code") { value("UNAUTHORIZED") }
        }
    }

    @Test
    fun `검증에 실패한 토큰은 401과 invalid_token으로 응답한다`() {
        mockMvc
            .get("/test/me") {
                header(HttpHeaders.AUTHORIZATION, "Bearer not-a-jwt")
            }.andExpect {
                status { isUnauthorized() }
                header { string(HttpHeaders.WWW_AUTHENTICATE, containsString("error=\"invalid_token\"")) }
                jsonPath("$.code") { value("UNAUTHORIZED") }
            }
    }

    @Test
    fun `유효한 토큰이면 sub와 roles로 인증한다`() {
        val token = issuer.issue(memberId = 123, roles = listOf("MEMBER")).value

        mockMvc
            .get("/test/me") {
                header(HttpHeaders.AUTHORIZATION, "Bearer $token")
            }.andExpect {
                status { isOk() }
                jsonPath("$.memberId") { value("123") }
                jsonPath("$.authorities[0]") { value("ROLE_MEMBER") }
            }
    }

    @Test
    fun `MEMBER 토큰으로 ADMIN API를 부르면 403 FORBIDDEN으로 응답한다`() {
        val token = issuer.issue(memberId = 123, roles = listOf("MEMBER")).value

        mockMvc
            .get("/api/v1/admin/test") {
                header(HttpHeaders.AUTHORIZATION, "Bearer $token")
            }.andExpect {
                status { isForbidden() }
                jsonPath("$.code") { value("FORBIDDEN") }
            }
    }

    @Test
    fun `JWKS는 인증 없이 공개키만 5분 캐시로 응답한다`() {
        mockMvc.get("/.well-known/jwks.json").andExpect {
            status { isOk() }
            header { string(HttpHeaders.CACHE_CONTROL, "max-age=300") }
            jsonPath("$.keys[0].kty") { value("RSA") }
            jsonPath("$.keys[0].alg") { value("RS256") }
            jsonPath("$.keys[0].use") { value("sig") }
            jsonPath("$.keys[0].kid") { exists() }
            jsonPath("$.keys[0].n") { exists() }
            jsonPath("$.keys[0].e") { exists() }
            jsonPath("$.keys[0].d") { doesNotExist() }
            jsonPath("$.keys[0].p") { doesNotExist() }
            jsonPath("$.keys[0].q") { doesNotExist() }
            content { string(not(containsString("\"d\""))) }
        }
    }

    @RestController
    class MeController {
        @GetMapping("/test/me")
        fun me(
            @AuthenticationPrincipal jwt: Jwt,
        ): Map<String, Any?> =
            mapOf(
                "memberId" to jwt.subject,
                "authorities" to
                    SecurityContextHolder
                        .getContext()
                        .authentication!!
                        .authorities
                        .map { it.authority },
            )

        @GetMapping("/api/v1/admin/test")
        fun adminOnly(): String = "ok"
    }
}
