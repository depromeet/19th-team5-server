package com.depromeet.core.auth.web

import com.depromeet.core.auth.AuthService
import com.depromeet.core.auth.AuthTokens
import jakarta.validation.Valid
import jakarta.validation.constraints.Email
import jakarta.validation.constraints.NotBlank
import jakarta.validation.constraints.NotNull
import jakarta.validation.constraints.Pattern
import jakarta.validation.constraints.Size
import org.springframework.http.HttpHeaders
import org.springframework.http.HttpStatus
import org.springframework.http.ResponseEntity
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RestController

/** 명세: 노션 인증/인가 API 명세 */
@RestController
@RequestMapping("/api/v1/auth")
class AuthController(
    private val authService: AuthService,
    private val refreshTokenCookie: RefreshTokenCookie,
) {
    @PostMapping("/signup")
    fun signup(
        @Valid @RequestBody request: SignupRequest,
    ): ResponseEntity<TokenResponse> = respond(HttpStatus.CREATED, authService.signup(request.email!!, request.password!!))

    @PostMapping("/login")
    fun login(
        @Valid @RequestBody request: LoginRequest,
    ): ResponseEntity<TokenResponse> = respond(HttpStatus.OK, authService.login(request.email!!, request.password!!))

    private fun respond(
        status: HttpStatus,
        tokens: AuthTokens,
    ): ResponseEntity<TokenResponse> =
        ResponseEntity
            .status(status)
            .header(HttpHeaders.SET_COOKIE, refreshTokenCookie.create(tokens.refreshToken).toString())
            .body(TokenResponse(accessToken = tokens.accessToken, expiresIn = tokens.expiresInSeconds))
}

/** 비밀번호 규칙(8~64자, 영문·숫자)은 400 INVALID_PASSWORD_FORMAT으로 따로 알려 주려고 서비스에서 검사한다 */
data class SignupRequest(
    // ASCII만 받는다. 유니코드는 소문자로 바꿀 때 길이가 늘거나 DB의 lower()와 결과가 달라질 수 있다
    @field:NotBlank @field:Email @field:Size(max = 254) @field:Pattern(regexp = "[\\x21-\\x7E]+", message = "영문·숫자·기호로 된 이메일만 쓸 수 있습니다")
    val email: String?,
    @field:NotNull
    val password: String?,
)

data class LoginRequest(
    @field:NotBlank
    val email: String?,
    @field:NotBlank
    val password: String?,
)

data class TokenResponse(
    val accessToken: String,
    val tokenType: String = "Bearer",
    val expiresIn: Long,
)
