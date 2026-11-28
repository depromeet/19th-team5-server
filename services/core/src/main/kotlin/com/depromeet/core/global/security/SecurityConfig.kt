package com.depromeet.core.global.security

import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.security.config.annotation.web.builders.HttpSecurity
import org.springframework.security.config.annotation.web.invoke
import org.springframework.security.config.http.SessionCreationPolicy
import org.springframework.security.oauth2.server.resource.authentication.JwtAuthenticationConverter
import org.springframework.security.oauth2.server.resource.authentication.JwtGrantedAuthoritiesConverter
import org.springframework.security.web.SecurityFilterChain

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
                authorize("/api/v1/admin/**", hasRole("ADMIN"))
                authorize(anyRequest, authenticated)
            }
            oauth2ResourceServer {
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
}
