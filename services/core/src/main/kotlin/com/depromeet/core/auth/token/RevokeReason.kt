package com.depromeet.core.auth.token

/** 리프레시 토큰 폐기 사유 (ADR-0004) */
enum class RevokeReason {
    ROTATED,
    LOGOUT,
    REUSE_DETECTED,
    PASSWORD_CHANGED,
    WITHDRAWN,
}
