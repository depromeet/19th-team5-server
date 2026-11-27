package com.depromeet.core.auth.jwt

/**
 * 서명 키를 가져오는 곳. 운영에서는 Secrets Manager에서 받아 메모리에만 둔다 (ADR-0010).
 * 운영용 구현은 보관소가 정해지면 추가한다. 구현이 없으면 core가 켜지지 않는다.
 */
fun interface SigningKeySource {
    fun load(): SigningKeys
}
