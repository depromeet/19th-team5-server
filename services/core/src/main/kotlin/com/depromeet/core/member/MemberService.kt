package com.depromeet.core.member

import com.depromeet.core.global.error.BusinessException
import com.depromeet.core.global.error.ErrorCode
import org.hibernate.exception.ConstraintViolationException
import org.slf4j.LoggerFactory
import org.springframework.dao.DataIntegrityViolationException
import org.springframework.security.crypto.password.PasswordEncoder
import org.springframework.stereotype.Service
import java.time.Clock

/**
 * 회원가입과 이메일·비밀번호 확인.
 * 해싱은 일부러 느린 계산(BCrypt cost 10, 약 0.1초)이라 트랜잭션 밖에서 한다. DB 연결을 붙잡지 않게 하려고.
 */
@Service
class MemberService(
    private val memberRepository: MemberRepository,
    private val passwordEncoder: PasswordEncoder,
    private val clock: Clock,
) {
    private val log = LoggerFactory.getLogger(javaClass)

    /** 없는 이메일도 이 해시와 비교해 "있는 이메일"과 응답 시간을 맞춘다 */
    private val dummyHash: String = checkNotNull(passwordEncoder.encode(DUMMY_PASSWORD))

    fun signup(
        email: String,
        rawPassword: String,
    ): Member {
        if (!PasswordPolicy.isValid(rawPassword)) throw BusinessException(ErrorCode.INVALID_PASSWORD_FORMAT)
        val hash = checkNotNull(passwordEncoder.encode(rawPassword))
        try {
            return memberRepository.save(Member(Member.normalizeEmail(email), hash, clock.instant()))
        } catch (e: DataIntegrityViolationException) {
            // 먼저 조회하지 않고 유니크 제약으로 막는다. 동시에 두 요청이 와도 하나만 들어간다
            if (violates(e, EMAIL_UNIQUE_CONSTRAINT)) throw BusinessException(ErrorCode.DUPLICATE_EMAIL)
            throw e
        }
    }

    /**
     * 이메일·비밀번호가 맞고 탈퇴하지 않은 회원을 돌려준다.
     * 없는 이메일, 틀린 비밀번호, 탈퇴 회원은 모두 같은 INVALID_CREDENTIALS다. 어느 쪽인지 알려 주지 않는다.
     */
    fun authenticate(
        email: String,
        rawPassword: String,
    ): Member {
        val member = memberRepository.findByEmail(Member.normalizeEmail(email))
        val hash = member?.passwordHash ?: dummyHash
        // 72바이트를 넘는 입력은 BCrypt에 넣지 않는다. 대신 더미 값으로 같은 시간만큼 계산한다
        val usable = PasswordPolicy.fitsHashInput(rawPassword)
        val matches = passwordEncoder.matches(if (usable) rawPassword else DUMMY_PASSWORD, hash) && usable

        if (member == null || !matches || member.status == MemberStatus.WITHDRAWN) {
            throw BusinessException(ErrorCode.INVALID_CREDENTIALS)
        }
        if (passwordEncoder.upgradeEncoding(hash)) {
            // 알고리즘이나 cost가 바뀐 옛 해시는 원문을 아는 지금 다시 저장한다 (ADR-0012)
            // 옮기기는 덤이다. 실패해도 로그인은 성공시키고 다음 로그인 때 다시 시도한다
            runCatching {
                val newHash = checkNotNull(passwordEncoder.encode(rawPassword))
                memberRepository.updatePasswordHashIfUnchanged(member.id!!, hash, newHash, clock.instant())
            }.onFailure { log.warn("비밀번호 해시 다시 저장 실패: memberId={}", member.id, it) }
        }
        return member
    }

    private fun violates(
        e: Throwable,
        constraint: String,
    ): Boolean =
        generateSequence(e) { it.cause }
            .filterIsInstance<ConstraintViolationException>()
            .any { it.constraintName.equals(constraint, ignoreCase = true) }

    companion object {
        const val EMAIL_UNIQUE_CONSTRAINT = "uk_member_email"
        private const val DUMMY_PASSWORD = "dummy-password-for-timing-1"
    }
}
