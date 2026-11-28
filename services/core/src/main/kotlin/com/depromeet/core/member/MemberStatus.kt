package com.depromeet.core.member

enum class MemberStatus {
    ACTIVE,

    /** 노쇼 2회. 로그인은 되고 예약이 막힌다 */
    RESTRICTED,

    /** 탈퇴. 로그인할 수 없다 */
    WITHDRAWN,
}
