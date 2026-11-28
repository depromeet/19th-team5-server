package com.depromeet.core.auth.token

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.EnumType
import jakarta.persistence.Enumerated
import jakarta.persistence.GeneratedValue
import jakarta.persistence.GenerationType
import jakarta.persistence.Id
import jakarta.persistence.Table
import java.time.Instant
import java.util.UUID

/** 원문은 쿠키로만 주고, 여기에는 SHA-256 해시만 저장한다 (ADR-0004) */
@Entity
@Table(name = "refresh_token")
class RefreshToken(
    familyId: UUID,
    memberId: Long,
    tokenHash: String,
    expiresAt: Instant,
    now: Instant,
) {
    @Id
    @GeneratedValue(strategy = GenerationType.IDENTITY)
    var id: Long? = null
        protected set

    @Column(nullable = false, updatable = false)
    var familyId: UUID = familyId
        protected set

    @Column(nullable = false, updatable = false)
    var memberId: Long = memberId
        protected set

    @Column(nullable = false, updatable = false)
    var tokenHash: String = tokenHash
        protected set

    @Column(nullable = false, updatable = false)
    var expiresAt: Instant = expiresAt
        protected set

    @Column(nullable = false, updatable = false)
    var createdAt: Instant = now
        protected set

    var revokedAt: Instant? = null
        protected set

    @Enumerated(EnumType.STRING)
    var revokeReason: RevokeReason? = null
        protected set
}
