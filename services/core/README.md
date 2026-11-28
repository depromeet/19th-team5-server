# core

핵심 API 서버입니다.

## 패키지

- `auth`: 인증
- `member`: 회원
- `reservation`: 예약
- `global`: 공통 (에러 응답, 보안 설정)

## 기술

- Kotlin 2.4 (JVM 21), Spring Boot 4.2, Gradle (Kotlin DSL)
- PostgreSQL 17, Flyway (`src/main/resources/db/migration`)
- 테스트: JUnit 5, Testcontainers

## 실행 방법

필요한 것: JDK 21, Docker (Kotlin은 Gradle이 받아 온다)

```bash
./gradlew bootRun
```

- `compose.yaml`의 PostgreSQL을 Spring Boot가 자동으로 띄우고 연결한다
- 헬스 체크: `GET /actuator/health`
- 프로필을 정하지 않으면 `local`로 켜진다. `local`은 켜질 때마다 메모리에서 임시 서명 키를 만들므로, 재시작하면 이전 액세스 토큰은 모두 무효가 된다
- 공개키 목록: `GET /.well-known/jwks.json`

## 테스트

```bash
./gradlew test
```

- DB가 필요한 테스트는 Testcontainers로 PostgreSQL을 띄우므로 Docker가 켜져 있어야 한다

## 코드 스타일

[Kotlin 공식 코딩 컨벤션](https://kotlinlang.org/docs/coding-conventions.html)을 따르고, Spotless + ktlint로 자동 정리한다.

```bash
./gradlew spotlessApply   # 규칙대로 자동 수정
./gradlew spotlessCheck   # 어긋나면 실패
```

- IntelliJ: Settings → Editor → Code Style → Kotlin → Set from… → Kotlin style guide
- 테스트 이름은 백틱으로 한글 문장을 쓴다. 예: `` fun `없는 경로는 404로 응답한다`() ``

## 에러 응답

Problem JSON(RFC 9457)에 `code`를 붙인다. 코드 목록은 `global/error/ErrorCode`, 기준은 API 명세(노션)다.

```json
{
  "status": 403,
  "title": "Forbidden",
  "detail": "다른 사람의 예약입니다.",
  "code": "FORBIDDEN"
}
```
