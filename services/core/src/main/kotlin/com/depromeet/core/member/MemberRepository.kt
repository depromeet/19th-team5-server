package com.depromeet.core.member

import org.springframework.data.jpa.repository.JpaRepository
import org.springframework.data.jpa.repository.Modifying
import org.springframework.data.jpa.repository.Query
import org.springframework.transaction.annotation.Transactional
import java.time.Instant

interface MemberRepository : JpaRepository<Member, Long> {
    fun findByEmail(email: String): Member?

    /**
     * 검증에 쓴 해시가 아직 그대로일 때만 바꾼다 (ADR-0012).
     * 그 사이 비밀번호가 바뀌었으면 0을 돌려주고 아무것도 바꾸지 않는다.
     */
    @Transactional
    @Modifying(clearAutomatically = true)
    @Query(
        """
        update Member m set m.passwordHash = :newHash, m.updatedAt = :now
        where m.id = :id and m.passwordHash = :oldHash
        """,
    )
    fun updatePasswordHashIfUnchanged(
        id: Long,
        oldHash: String,
        newHash: String,
        now: Instant,
    ): Int
}
