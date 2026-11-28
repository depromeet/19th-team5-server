package com.depromeet.core.member

/**
 * 비밀번호 규칙. 명세: 8~64자, 영문·숫자 포함.
 * BCrypt는 앞 72바이트만 쓰므로 UTF-8 72바이트도 넘지 않아야 한다. 한글은 24자에서 넘는다.
 */
object PasswordPolicy {
    const val MIN_LENGTH = 8
    const val MAX_LENGTH = 64
    const val MAX_BYTES = 72

    private val LETTER = Regex("[A-Za-z]")
    private val DIGIT = Regex("[0-9]")

    fun isValid(password: String): Boolean {
        val length = password.codePointCount(0, password.length)
        return length in MIN_LENGTH..MAX_LENGTH &&
            fitsHashInput(password) &&
            LETTER.containsMatchIn(password) &&
            DIGIT.containsMatchIn(password)
    }

    /** BCrypt에 넣어도 잘리지 않는 길이인지 */
    fun fitsHashInput(password: String): Boolean = password.toByteArray(Charsets.UTF_8).size <= MAX_BYTES
}
