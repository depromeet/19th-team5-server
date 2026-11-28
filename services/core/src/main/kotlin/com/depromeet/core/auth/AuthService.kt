package com.depromeet.core.auth

import com.depromeet.core.auth.jwt.AccessTokenIssuer
import com.depromeet.core.auth.jwt.JwtProperties
import com.depromeet.core.auth.token.IssuedRefreshToken
import com.depromeet.core.auth.token.RefreshTokenIssuer
import com.depromeet.core.member.Member
import com.depromeet.core.member.MemberService
import org.springframework.stereotype.Service

data class AuthTokens(
    val accessToken: String,
    val expiresInSeconds: Long,
    val refreshToken: IssuedRefreshToken,
)

/**
 * 가입·로그인에 성공하면 액세스 토큰과 새 묶음의 리프레시 토큰을 준다.
 * 회원 저장과 토큰 저장은 트랜잭션을 나눈다. 토큰 저장이 실패해도 가입은 남고, 다시 로그인하면 된다.
 */
@Service
class AuthService(
    private val memberService: MemberService,
    private val accessTokenIssuer: AccessTokenIssuer,
    private val refreshTokenIssuer: RefreshTokenIssuer,
    private val jwtProperties: JwtProperties,
) {
    fun signup(
        email: String,
        password: String,
    ): AuthTokens = issue(memberService.signup(email, password))

    fun login(
        email: String,
        password: String,
    ): AuthTokens = issue(memberService.authenticate(email, password))

    private fun issue(member: Member): AuthTokens {
        val memberId = member.id!!
        val access = accessTokenIssuer.issue(memberId, listOf(member.role.name))
        return AuthTokens(
            accessToken = access.value,
            expiresInSeconds = jwtProperties.accessTokenTtl.seconds,
            refreshToken = refreshTokenIssuer.issueNewFamily(memberId),
        )
    }
}
