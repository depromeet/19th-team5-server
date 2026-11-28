package com.depromeet.core.global.security

import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.security.crypto.factory.PasswordEncoderFactories
import org.springframework.security.crypto.password.PasswordEncoder

@Configuration
class PasswordConfig {
    /**
     * 해시 앞에 `{bcrypt}` 이름표를 붙인다 (ADR-0012). 기본은 BCrypt cost 10.
     * 알고리즘은 배포 서버에서 측정해 정하고, 바꾸면 옛 해시는 로그인할 때 새 방식으로 옮겨진다.
     */
    @Bean
    fun passwordEncoder(): PasswordEncoder = PasswordEncoderFactories.createDelegatingPasswordEncoder()
}
