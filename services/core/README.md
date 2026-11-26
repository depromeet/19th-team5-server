# core

핵심 API 서버입니다.

## 패키지

- `auth`: 인증
- `member`: 회원
- `reservation`: 예약
- `global`: 공통 (에러 응답, 보안 설정)

## 기술

- Java 21, Spring Boot 4.2, Gradle (Kotlin DSL)
- PostgreSQL 17, Flyway (`src/main/resources/db/migration`)
- 테스트: JUnit 5, Testcontainers

## 실행 방법

필요한 것: JDK 21, Docker

```bash
./gradlew bootRun
```

- `compose.yaml`의 PostgreSQL을 Spring Boot가 자동으로 띄우고 연결한다
- 헬스 체크: `GET /actuator/health`

## 테스트

```bash
./gradlew test
```

- DB가 필요한 테스트는 Testcontainers로 PostgreSQL을 띄우므로 Docker가 켜져 있어야 한다

## 코드 스타일

[네이버 핵데이 Java 컨벤션](https://github.com/naver/hackday-conventions-java)을 따르고, Spotless로 자동 정리한다. (탭 들여쓰기, 한 줄 120자, import 순서 static → java → javax → org → net → com)

```bash
./gradlew spotlessApply   # 규칙대로 자동 수정
./gradlew spotlessCheck   # 어긋나면 실패
```

- IntelliJ: `config/naver-intellij-formatter.xml`을 Settings → Editor → Code Style → Java → Import Scheme으로 불러온다

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
