package com.depromeet.core.auth.token

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.GeneratedValue
import jakarta.persistence.GenerationType
import jakarta.persistence.Id
import jakarta.persistence.Table
import java.time.Instant
import java.util.UUID

/** 로그인 1번 = 묶음 1개. 재사용이 감지되면 묶음째 폐기한다 (ADR-0004) */
@Entity
@Table(name = "refresh_token_family")
class RefreshTokenFamily(
    memberId: Long,
    now: Instant,
) {
    @Id
    @GeneratedValue(strategy = GenerationType.UUID)
    var id: UUID? = null
        protected set

    @Column(nullable = false, updatable = false)
    var memberId: Long = memberId
        protected set

    @Column(nullable = false, updatable = false)
    var createdAt: Instant = now
        protected set
}
