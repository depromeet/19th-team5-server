package com.depromeet.core.auth.jwt

import com.nimbusds.jose.jwk.gen.RSAKeyGenerator
import org.slf4j.LoggerFactory
import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Component

/**
 * 로컬 개발과 테스트용. 켜질 때마다 새 키를 메모리에서 만든다.
 * 레포와 디스크에 키를 두지 않기 위해서다. 재시작하면 이전 액세스 토큰은 모두 무효가 된다.
 */
@Component
@Profile("local")
class EphemeralSigningKeySource : SigningKeySource {
    private val log = LoggerFactory.getLogger(javaClass)

    override fun load(): SigningKeys {
        log.warn("로컬용 임시 서명 키를 만듭니다. 운영에서는 쓰지 마세요.")
        val key = RSAKeyGenerator(SigningKeys.MIN_RSA_BITS).keyID(KID).generate()
        return SigningKeys(listOf(key), signingKid = KID)
    }

    companion object {
        const val KID = "local-key1"
    }
}
