package com.depromeet.core.global.error

class BusinessException(
    val errorCode: ErrorCode,
    detail: String = errorCode.message,
) : RuntimeException(detail)
