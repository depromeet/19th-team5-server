package com.depromeet.core.global.security

import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.security.config.annotation.web.builders.HttpSecurity
import org.springframework.security.config.annotation.web.invoke
import org.springframework.security.config.http.SessionCreationPolicy
import org.springframework.security.oauth2.server.resource.authentication.JwtAuthenticationConverter
import org.springframework.security.oauth2.server.resource.authentication.JwtGrantedAuthoritiesConverter
import org.springframework.security.oauth2.server.resource.web.BearerTokenResolver
import org.springframework.security.oauth2.server.resource.web.DefaultBearerTokenResolver
import org.springframework.security.web.SecurityFilterChain
import org.springframework.security.web.servlet.util.matcher.PathPatternRequestMatcher

/**
 * 세션 없이 `Authorization: Bearer` 액세스 토큰으로 인증한다 (RFC 6750).
 * 토큰 검증 규칙은 auth.jwt.JwtConfig에 있다.
 */
@Configuration
class SecurityConfig(
    private val problemHandler: ProblemSecurityHandler,
) {
    @Bean
    fun securityFilterChain(http: HttpSecurity): SecurityFilterChain {
        http {
            csrf { disable() }
            formLogin { disable() }
            httpBasic { disable() }
            logout { disable() }
            sessionManagement { sessionCreationPolicy = SessionCreationPolicy.STATELESS }
            authorizeHttpRequests {
                authorize("/actuator/health", permitAll)
                authorize("/.well-known/jwks.json", permitAll)
                TOKENLESS_PATHS.forEach { authorize(it, permitAll) }
                authorize("/api/v1/admin/**", hasRole("ADMIN"))
                authorize(anyRequest, authenticated)
            }
            oauth2ResourceServer {
                bearerTokenResolver = bearerTokenResolver()
                jwt { jwtAuthenticationConverter = rolesToAuthorities() }
                authenticationEntryPoint = problemHandler
                accessDeniedHandler = problemHandler
            }
            exceptionHandling {
                authenticationEntryPoint = problemHandler
                accessDeniedHandler = problemHandler
            }
        }
        return http.build()
    }

    /**
     * 액세스 토큰 없이 부르는 경로에서는 Authorization 헤더를 읽지 않는다.
     * 읽으면 만료 토큰이 붙어 온 로그인·재발급이 401 TOKEN_EXPIRED가 되고, 프론트는 재발급을 다시 부르며 맴돈다.
     */
    private fun bearerTokenResolver(): BearerTokenResolver {
        val default = DefaultBearerTokenResolver()
        val tokenless = TOKENLESS_PATHS.map { PathPatternRequestMatcher.pathPattern(it) }
        return BearerTokenResolver { request ->
            if (tokenless.any { it.matches(request) }) null else default.resolve(request)
        }
    }

    /** `roles: ["MEMBER"]` → `ROLE_MEMBER`. 회원 ID는 `sub`에서 꺼낸다. */
    private fun rolesToAuthorities(): JwtAuthenticationConverter =
        JwtAuthenticationConverter().apply {
            setJwtGrantedAuthoritiesConverter(
                JwtGrantedAuthoritiesConverter().apply {
                    setAuthoritiesClaimName("roles")
                    setAuthorityPrefix("ROLE_")
                },
            )
        }

    companion object {
        /** 액세스 토큰 없이 부르는 인증 API. 재발급·로그아웃은 만들 때 추가한다 */
        val TOKENLESS_PATHS = listOf("/api/v1/auth/signup", "/api/v1/auth/login")
    }
}
