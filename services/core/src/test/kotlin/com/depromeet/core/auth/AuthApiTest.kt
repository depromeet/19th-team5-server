package com.depromeet.core.auth

import com.depromeet.core.TestcontainersConfiguration
import com.depromeet.core.auth.token.RefreshTokenIssuer
import com.depromeet.core.global.error.BusinessException
import com.depromeet.core.global.error.ErrorCode
import com.depromeet.core.member.MemberService
import org.assertj.core.api.Assertions.assertThat
import org.hamcrest.Matchers.containsString
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc
import org.springframework.context.annotation.Import
import org.springframework.http.HttpHeaders
import org.springframework.http.MediaType
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.security.crypto.bcrypt.BCryptPasswordEncoder
import org.springframework.security.oauth2.jwt.JwtDecoder
import org.springframework.test.web.servlet.MockMvc
import org.springframework.test.web.servlet.ResultActionsDsl
import org.springframework.test.web.servlet.post
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

@Import(TestcontainersConfiguration::class)
@SpringBootTest
@AutoConfigureMockMvc
class AuthApiTest {
    @Autowired
    lateinit var mockMvc: MockMvc

    @Autowired
    lateinit var jdbc: JdbcTemplate

    @Autowired
    lateinit var jwtDecoder: JwtDecoder

    @Autowired
    lateinit var memberService: MemberService

    @BeforeEach
    fun clean() {
        jdbc.execute("TRUNCATE refresh_token, refresh_token_family, member RESTART IDENTITY CASCADE")
    }

    // 가입

    @Test
    fun `가입하면 201과 토큰 응답, 리프레시 쿠키를 준다`() {
        val result =
            signup("user@example.com", "abcd1234!")
                .andExpect {
                    status { isCreated() }
                    jsonPath("$.tokenType") { value("Bearer") }
                    jsonPath("$.expiresIn") { value(1800) }
                    jsonPath("$.accessToken") { exists() }
                    header { string(HttpHeaders.SET_COOKIE, containsString("refresh_token=")) }
                    header { string(HttpHeaders.SET_COOKIE, containsString("Path=/api/v1/auth")) }
                    header { string(HttpHeaders.SET_COOKIE, containsString("Max-Age=1209600")) }
                    header { string(HttpHeaders.SET_COOKIE, containsString("Secure")) }
                    header { string(HttpHeaders.SET_COOKIE, containsString("HttpOnly")) }
                    header { string(HttpHeaders.SET_COOKIE, containsString("SameSite=Strict")) }
                }.andReturn()

        val memberId = jdbc.queryForObject("SELECT id FROM member WHERE email = 'user@example.com'", Long::class.java)
        val accessToken = Regex("\"accessToken\":\"([^\"]+)\"").find(result.response.contentAsString)!!.groupValues[1]
        val jwt = jwtDecoder.decode(accessToken)
        assertThat(jwt.subject).isEqualTo(memberId.toString())
        assertThat(jwt.getClaimAsStringList("roles")).containsExactly("MEMBER")
    }

    @Test
    fun `비밀번호는 {bcrypt} 이름표를 붙인 해시로, 리프레시 토큰은 SHA-256 해시로만 저장한다`() {
        val cookie = refreshCookie(signup("user@example.com", "abcd1234!"))

        val passwordHash = jdbc.queryForObject("SELECT password_hash FROM member", String::class.java)!!
        assertThat(passwordHash).startsWith("{bcrypt}\$2a\$10\$").doesNotContain("abcd1234!")

        val hashes = jdbc.queryForList("SELECT token_hash FROM refresh_token", String::class.java)
        assertThat(hashes).containsExactly(RefreshTokenIssuer.hash(cookie))
        assertThat(hashes.single()).hasSize(64).isNotEqualTo(cookie)
    }

    @Test
    fun `이메일은 소문자로 저장하고, 대소문자만 다른 이메일은 중복이다`() {
        signup("User@Example.COM", "abcd1234!").andExpect { status { isCreated() } }

        assertThat(jdbc.queryForObject("SELECT email FROM member", String::class.java)).isEqualTo("user@example.com")
        signup("user@example.com", "abcd1234!").andExpect {
            status { isConflict() }
            jsonPath("$.code") { value("DUPLICATE_EMAIL") }
        }
    }

    @Test
    fun `이메일 형식이 틀리면 400 INVALID_REQUEST와 invalidParams를 준다`() {
        signup("not-an-email", "abcd1234!").andExpect {
            status { isBadRequest() }
            jsonPath("$.code") { value("INVALID_REQUEST") }
            jsonPath("$.invalidParams[0].name") { value("email") }
        }
    }

    @Test
    fun `ASCII가 아닌 이메일은 400 INVALID_REQUEST다`() {
        signup("사용자@example.com", "abcd1234!").andExpect {
            status { isBadRequest() }
            jsonPath("$.code") { value("INVALID_REQUEST") }
            jsonPath("$.invalidParams[0].name") { value("email") }
        }
    }

    @Test
    fun `비밀번호 규칙에 맞지 않으면 400 INVALID_PASSWORD_FORMAT을 준다`() {
        signup("user@example.com", "abcdefgh").andExpect {
            status { isBadRequest() }
            jsonPath("$.code") { value("INVALID_PASSWORD_FORMAT") }
        }
        assertThat(jdbc.queryForObject("SELECT count(*) FROM member", Int::class.java)).isZero()
    }

    @Test
    fun `같은 이메일로 동시에 가입하면 하나만 성공하고 나머지는 DUPLICATE_EMAIL이다`() {
        val threads = 5
        val ready = CountDownLatch(threads)
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(threads)
        val results =
            try {
                (1..threads)
                    .map {
                        pool.submit(
                            Callable {
                                ready.countDown()
                                start.await()
                                runCatching { memberService.signup("race@example.com", "abcd1234!") }
                            },
                        )
                    }.also {
                        ready.await()
                        start.countDown()
                    }.map { it.get(30, TimeUnit.SECONDS) }
            } finally {
                pool.shutdownNow()
            }

        assertThat(results.count { it.isSuccess }).isEqualTo(1)
        val failures = results.mapNotNull { it.exceptionOrNull() }
        assertThat(failures).hasSize(threads - 1).allSatisfy {
            assertThat(it).isInstanceOf(BusinessException::class.java)
            assertThat((it as BusinessException).errorCode).isEqualTo(ErrorCode.DUPLICATE_EMAIL)
        }
    }

    // 로그인

    @Test
    fun `로그인하면 200과 토큰 응답, 리프레시 쿠키를 주고 로그인마다 새 묶음을 만든다`() {
        signup("user@example.com", "abcd1234!")

        login("USER@example.com", "abcd1234!").andExpect {
            status { isOk() }
            jsonPath("$.tokenType") { value("Bearer") }
            jsonPath("$.expiresIn") { value(1800) }
            header { string(HttpHeaders.SET_COOKIE, containsString("refresh_token=")) }
        }

        assertThat(jdbc.queryForObject("SELECT count(DISTINCT family_id) FROM refresh_token", Int::class.java)).isEqualTo(2)
    }

    @Test
    fun `틀린 비밀번호와 없는 이메일은 같은 401 INVALID_CREDENTIALS를 준다`() {
        signup("user@example.com", "abcd1234!")

        val wrongPassword = login("user@example.com", "wrong1234").andReturn().response
        val unknownEmail = login("nobody@example.com", "abcd1234!").andReturn().response

        assertThat(wrongPassword.status).isEqualTo(401)
        assertThat(unknownEmail.status).isEqualTo(401)
        assertThat(wrongPassword.contentAsString).contains("\"code\":\"INVALID_CREDENTIALS\"")
        assertThat(unknownEmail.contentAsString).isEqualTo(wrongPassword.contentAsString)
        assertThat(wrongPassword.getHeader(HttpHeaders.SET_COOKIE)).isNull()
    }

    @Test
    fun `72바이트를 넘는 비밀번호로 로그인하면 401 INVALID_CREDENTIALS다`() {
        signup("user@example.com", "abcd1234!")

        login("user@example.com", "a1" + "가".repeat(30)).andExpect {
            status { isUnauthorized() }
            jsonPath("$.code") { value("INVALID_CREDENTIALS") }
        }
    }

    @Test
    fun `탈퇴한 회원은 비밀번호가 맞아도 401 INVALID_CREDENTIALS다`() {
        signup("user@example.com", "abcd1234!")
        jdbc.update("UPDATE member SET status = 'WITHDRAWN'")

        login("user@example.com", "abcd1234!").andExpect {
            status { isUnauthorized() }
            jsonPath("$.code") { value("INVALID_CREDENTIALS") }
        }
    }

    @Test
    fun `옛 설정으로 만든 해시는 로그인에 성공하면 지금 설정으로 다시 저장한다`() {
        signup("user@example.com", "abcd1234!")
        val oldHash = "{bcrypt}" + BCryptPasswordEncoder(4).encode("abcd1234!")
        jdbc.update("UPDATE member SET password_hash = ?", oldHash)

        login("user@example.com", "abcd1234!").andExpect { status { isOk() } }

        val newHash = jdbc.queryForObject("SELECT password_hash FROM member", String::class.java)!!
        assertThat(newHash).isNotEqualTo(oldHash).startsWith("{bcrypt}\$2a\$10\$")
        login("user@example.com", "abcd1234!").andExpect { status { isOk() } }
    }

    @Test
    fun `로그인과 가입은 만료되거나 잘못된 Bearer 토큰이 붙어 와도 무시한다`() {
        signup("user@example.com", "abcd1234!", bearer = "not-a-jwt").andExpect { status { isCreated() } }
        login("user@example.com", "abcd1234!", bearer = "not-a-jwt").andExpect { status { isOk() } }
    }

    private fun signup(
        email: String,
        password: String,
        bearer: String? = null,
    ): ResultActionsDsl = post("/api/v1/auth/signup", email, password, bearer)

    private fun login(
        email: String,
        password: String,
        bearer: String? = null,
    ): ResultActionsDsl = post("/api/v1/auth/login", email, password, bearer)

    private fun post(
        url: String,
        email: String,
        password: String,
        bearer: String?,
    ): ResultActionsDsl =
        mockMvc.post(url) {
            contentType = MediaType.APPLICATION_JSON
            content = """{"email":"$email","password":"$password"}"""
            bearer?.let { header(HttpHeaders.AUTHORIZATION, "Bearer $it") }
        }

    private fun refreshCookie(actions: ResultActionsDsl): String =
        actions
            .andReturn()
            .response
            .getHeader(HttpHeaders.SET_COOKIE)!!
            .substringAfter("refresh_token=")
            .substringBefore(";")
}
