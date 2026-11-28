package com.depromeet.core.member

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.EnumType
import jakarta.persistence.Enumerated
import jakarta.persistence.GeneratedValue
import jakarta.persistence.GenerationType
import jakarta.persistence.Id
import jakarta.persistence.Table
import java.time.Instant

@Entity
@Table(name = "member")
class Member(
    email: String,
    passwordHash: String,
    now: Instant,
) {
    @Id
    @GeneratedValue(strategy = GenerationType.IDENTITY)
    var id: Long? = null
        protected set

    @Column(nullable = false, updatable = false)
    var email: String = email
        protected set

    @Column(nullable = false)
    var passwordHash: String = passwordHash
        protected set

    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    var role: MemberRole = MemberRole.MEMBER
        protected set

    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    var status: MemberStatus = MemberStatus.ACTIVE
        protected set

    @Column(nullable = false, updatable = false)
    var createdAt: Instant = now
        protected set

    @Column(nullable = false)
    var updatedAt: Instant = now
        protected set

    companion object {
        /** 같은 주소를 대소문자만 바꿔 두 번 가입하지 못하게 소문자로 저장하고 찾는다 */
        fun normalizeEmail(email: String): String = email.lowercase()
    }
}
