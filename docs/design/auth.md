# 인증/인가 설계

담당: 노을 | 최종 갱신: 2026-11-24

## 개요

core만 토큰을 만들고(개인키 서명), realtime은 공개키로 혼자 검증한다. 브라우저는 core에서 30초짜리 일회용 티켓을 받아 realtime WebSocket에 연결한다. 방 안의 역할(면접관/면접자)은 토큰이 아니라 예약 데이터에서 gRPC로 받는다.

## 목표와 비목표

**목표**

- 발급은 core 한 곳에서만 하고, realtime은 core에 묻지 않고 혼자 검증한다. 연결이 붙은 뒤에는 core가 잠깐 멈춰도 진행 중인 면접은 영향을 받지 않는다
- 검증하는 서버(realtime)는 토큰을 위조할 수 없다. 서명 키는 core만 가진다
- 인증되지 않은 WebSocket 연결은 열지 않는다
- 같은 사람이 방마다 면접관도, 면접자도 될 수 있게 방 단위로 권한을 판단한다. 시간 조건이 붙은 권한(예: 시작 1시간 전부터 면접관만 자소서 열람)도 판단한다
- 상대 참가자에게는 닉네임만 보이고 회원 ID, 이메일, IP는 노출하지 않는다
- 토큰이 탈취돼도 피해 범위를 줄인다. 액세스 토큰은 최대 30분, WebSocket 티켓은 30초 한 번만 쓸 수 있고, 리프레시 토큰은 재사용을 감지하면 바로 막는다
- 로그인이 몰려도 다른 API는 느려지지 않는다
- Redis 없이 서버 한 대와 PostgreSQL만으로 시작한다

**비목표**

- 소셜 로그인, 이메일 인증, 메일로 비밀번호 찾기
- 로그인 실패 시 계정 잠금 (대신 요청 제한)
- 로그아웃·정지 시 액세스 토큰 즉시 차단 (블랙리스트)
- 비회원(게스트) 토큰. 로비는 인증 없이 조회한다
- 리프레시 토큰을 기기에 묶기 (DPoP)
- 별도 인증 서버 분리, API 게이트웨이
- realtime 여러 대 운영 (티켓 재사용 방지를 메모리로 하기 때문)
- 음성 연결 인증의 세부 구현. realtime → 음성 경계는 음성 설계 문서가 맡는다

## 서버별 역할

- core (Java)
  - `member`: 회원가입, 내 정보, 비밀번호 변경, 탈퇴, 계정 상태
  - `auth`: 로그인, 액세스 토큰 발급·재발급·로그아웃, WebSocket 티켓 발급, JWKS 공개, 요청마다 액세스 토큰 검증, 로그인 요청 제한
  - `reservation`: 예약, 방 역할 저장, realtime에 참가자 목록 제공 (gRPC)
- realtime (Go)
  - WebSocket 업그레이드 전에 티켓 검증
  - 방 안 권한 판단 (면접 시작 때 받은 방 역할로)
  - v1은 음성(미디어)과 한 프로세스라 음성 시그널링도 이 인증을 그대로 쓴다
- PostgreSQL: 회원, 리프레시 토큰(해시), 예약(방 역할)

core 안의 모듈끼리 주고받는 것:

| 보내는 쪽 → 받는 쪽 | 내용 |
| --- | --- |
| `auth` → `member` | 이메일·비밀번호 확인 → 회원 ID, 역할, 상태 |
| `member` → `auth` | 비밀번호 변경·탈퇴 시 리프레시 토큰 전부 폐기 |
| `reservation` → `member` | 예약 시 이용 제한 여부, 면접권 확인 |
| realtime → `reservation` | gRPC `GetParticipants` |

## 흐름

### 토큰 세 가지

| | 액세스 토큰 | WebSocket 티켓 | 리프레시 토큰 |
| --- | --- | --- | --- |
| 형식 | JWT (RS256) | JWT (RS256) | 랜덤 32바이트 |
| `typ` | `at+jwt` | `ws-ticket+jwt` | - |
| 받는 곳 (`aud`) | core | realtime | core (`/api/v1/auth`) |
| 수명 | 30분 | 30초, 한 번만 | 14일 |
| 브라우저 보관 | JS 메모리 | 받자마자 연결에 사용 | HttpOnly 쿠키 |
| 서버 보관 | 없음 | realtime이 쓴 `jti`를 만료 + 시계 여유까지 기억 | DB에 SHA-256 해시 |

```json
// 액세스 토큰
헤더  { "alg": "RS256", "typ": "at+jwt", "kid": "2026-11-key1" }
내용  {
  "iss": "https://api.도메인",
  "aud": ["https://api.도메인"],
  "sub": "123",
  "client_id": "web",
  "roles": ["MEMBER"],
  "iat": 1763450000,
  "exp": 1763451800,
  "jti": "uuid"
}

// WebSocket 티켓
헤더  { "alg": "RS256", "typ": "ws-ticket+jwt", "kid": "2026-11-key1" }
내용  {
  "iss": "https://api.도메인",
  "aud": ["wss://rt.도메인"],
  "sub": "123",
  "iat": 1763450000,
  "exp": 1763450030,
  "jti": "uuid"
}
```

- 이메일·이름 같은 개인정보는 넣지 않는다 (JWT는 서명만 하고 암호화하지 않는다)
- `typ`와 `aud`가 달라서 티켓을 액세스 토큰으로 쓸 수 없고, 반대도 안 된다
- `scope`는 쓰지 않는다

### 로그인

```
브라우저 → core: POST /api/v1/auth/login (이메일, 비밀번호)
core: 요청 제한 확인 (계정별 엄격, IP별 느슨) → 넘으면 429
core: 해싱 자리 확보 (동시 N개, 최대 1초 대기) → 없으면 503 SERVICE_BUSY + Retry-After: 1
core: 비밀번호 비교 (없는 이메일도 더미 해시와 비교해 응답 시간을 맞춘다)
core: 리프레시 토큰 생성 (새 family_id) → DB에 해시 저장
core → 브라우저: 액세스 토큰(본문) + 리프레시 토큰(쿠키)
```

- 액세스 토큰은 JS 메모리에만 둔다. localStorage·sessionStorage에 저장하지 않는다
- 리프레시 쿠키: `HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/api/v1/auth`

### 토큰 검증 (core, realtime 공통)

1. 키는 `GET /.well-known/jwks.json`에서만 받는다. 토큰 헤더의 `jku`, `x5u`는 무시한다
2. 헤더 `kid`로 JWKS 안에서 공개키를 고른다. 목록에 없으면 거절한다
3. 아래 순서로 확인한다 (RFC 9068 4장 순서)

| 순서 | 항목 | 규칙 |
| --- | --- | --- |
| 1 | `typ` | core는 `at+jwt`, realtime은 `ws-ticket+jwt`만 받는다 |
| 2 | `iss` | core 주소와 정확히 같아야 함 |
| 3 | `aud` | 자기 서버 값이 들어 있어야 함 |
| 4 | 서명 | `alg`는 서버에서 RS256으로 고정. `none`, HS256 거절 |
| 5 | `exp` | 시계 차이 여유 최대 60초 |

- core는 자기 JWKS를 HTTP로 다시 읽지 않고 메모리의 공개키로 검증한다
- Spring `NimbusJwtDecoder`는 기본 설정에서 `typ: at+jwt`를 거부한다. `DefaultJOSEObjectTypeVerifier`로 `at+jwt`를 허용해야 한다
- golang-jwt는 `typ` 검사 옵션이 없어서 `token.Header["typ"]`를 직접 확인한다

**JWKS 캐시 (realtime)**

| 규칙 | 값 |
| --- | --- |
| 주기 갱신 | 5분마다 다시 받는다 (JWKS에서 빠진 키를 메모리에서도 지우려고) |
| 모르는 `kid` | 한 번 다시 받고, 그래도 없으면 거절 |
| 다시 받기 제한 | 1분에 한 번까지 |

- core는 JWKS 응답에 `Cache-Control: max-age=300`을 붙인다

### 재발급

```
브라우저 → core: POST /api/v1/auth/reissue (리프레시 쿠키)
[트랜잭션]
core: 쿠키 값 → SHA-256 → DB 조회 (SELECT ... FOR UPDATE)
  ├ 없음 또는 만료                             → 401 REFRESH_TOKEN_INVALID
  ├ 폐기됨, 사유 ROTATED, 폐기 10초 이내        → 409 REFRESH_IN_PROGRESS (새 토큰은 주지 않는다)
  ├ 폐기됨, 그 밖                              → 같은 family_id 전부 폐기(REUSE_DETECTED) → 401
  └ 정상 → 옛 토큰 폐기(ROTATED), 같은 family_id로 새 토큰 저장 → 새 액세스 토큰 + 새 쿠키
```

- `revoke_reason`: `ROTATED`, `LOGOUT`, `REUSE_DETECTED`, `PASSWORD_CHANGED`, `WITHDRAWN`. 10초 유예는 `ROTATED`에만 준다
- 재사용 감지 때 family 폐기는 401 예외와 함께 롤백되지 않게 별도 트랜잭션(`REQUIRES_NEW`)으로 커밋한다
- 브라우저는 `navigator.locks`로 한 번에 한 탭만 재발급한다. 409를 받은 탭은 1회 재시도한다 (새 쿠키가 이미 실려 성공한다)
- 새 리프레시 토큰의 만료 시각(첫 로그인 + 14일 고정 / 지금 + 14일 연장)은 구현 때 정한다
- 비밀번호 변경·탈퇴 시 그 회원의 리프레시 토큰을 전부 폐기한다

### 로그아웃

- 리프레시 토큰만 폐기(`LOGOUT`)하고 쿠키를 지운다. 액세스 토큰은 만료(최대 30분)까지 유효하다
- 이미 로그아웃된 상태여도 204

### WebSocket 연결 (티켓)

```
1. 브라우저 → core: POST /api/v1/auth/ws-ticket (Authorization: Bearer 액세스 토큰)
2. core → 브라우저: 30초짜리 일회용 티켓
3. 브라우저 → realtime: /ws?ticket=티켓
4. realtime: 업그레이드 전에 "토큰 검증" 규칙으로 티켓 확인 + jti가 처음 쓰이는지 확인 (exp + 60초까지 메모리에 기억)
   ├ 성공 → 101 업그레이드, 연결 시작
   └ 실패 → HTTP 401로 거절 (브라우저 onclose에는 1006만 보인다)
```

- 연결이 붙은 뒤에는 티켓이나 액세스 토큰이 만료돼도 연결을 유지한다
- 브라우저 재연결 규칙
  - 열리기 전에 1006으로 끊기면 티켓을 새로 받아 다시 연결한다. 간격은 1초, 2초, 4초처럼 늘리고 무작위 지연(jitter)을 더하며 최대 횟수를 둔다
  - 티켓 발급이 401이면 액세스 토큰을 재발급하고, 그것도 실패하면 로그인 화면으로 보낸다
- 연결 후 오류는 종료 코드로 알린다: `4001 INVALID_TOKEN`, `4002 TOKEN_EXPIRED`, `4003 FORBIDDEN` (realtime과 최종 확인)
- 티켓이 URL에 실리므로 realtime 접근 로그에는 쿼리 문자열을 남기지 않는다
- 인증된 연결에만 음성 시그널링(`voice.*`)과 TURN 임시 자격 증명을 허용한다

### 방 역할 받아오기 (gRPC)

```
1. 예약할 때 역할(면접관/면접자)이 reservation에 저장된다
2. 면접 시작 → realtime → core: GetParticipants(interview_id) + 서비스 토큰(메타데이터)
3. core: 서비스 토큰 확인 → 방이 지금 조회할 수 있는 시간인지 확인 → 참가자 목록
4. realtime: 메모리에 보관하고 면접이 끝날 때까지 방 안 권한 판단에 쓴다
```

| 상황 | gRPC 상태 |
| --- | --- |
| 서비스 토큰 없음·틀림 | `UNAUTHENTICATED` |
| 방이 시작 전이거나 끝남 | `FAILED_PRECONDITION` |

- 서비스 토큰은 32바이트 랜덤 값. core는 일정 시간 비교(`MessageDigest.isEqual`)로 확인하고, 교체 중에는 새 토큰과 옛 토큰 두 개를 받는다
- 화면·채팅·음성 메시지에는 회원 ID 대신 realtime이 입장 시 부여하는 방 참가자 ID(예: `p7`)를 쓴다
- 호출 방향은 realtime → core 한쪽만이다

### 서명 키

| 단계 | 방법 |
| --- | --- |
| 생성 | 담당자가 `openssl`로 RSA 2048비트 생성. 개발 키와 운영 키는 따로 |
| 보관 | Secrets Manager. 레포·이미지·환경 변수 값·Notion·Discord에는 두지 않는다 |
| 사용 | core가 켜질 때 IAM 권한으로 받아 메모리에만 둔다. 디스크에 파일을 만들지 않는다 |
| 교체 | 6개월마다 새 `kid`로 수동 교체. 옛 공개키는 JWKS에 31분(액세스 수명 + 60초) 이상 남긴다 |
| 유출 | 새 키로 서명 시작 → 옛 `kid`를 JWKS에서 즉시 제거 → realtime 재시작 (메모리의 옛 키를 버리려고) |

- 키가 바뀌어도 리프레시 토큰은 서명 키와 무관해서, 사용자는 재로그인 없이 재발급만 받으면 된다
- JWKS 응답에는 `n`, `e`만 있고 개인키 값(`d`, `p`, `q`)은 없어야 한다 (테스트로 확인)

### 비밀번호와 요청 제한

- 해시 앞에 알고리즘 이름표를 붙인다: `{bcrypt}$2a$10$...` (Spring `DelegatingPasswordEncoder`)
- 알고리즘(BCrypt cost 10 / Argon2id 19MiB·2회·병렬 1)은 배포 서버에서 측정해 정한다
  - BCrypt를 고르면 비밀번호 최대 길이를 바이트로도 검사한다 (72바이트 제한, 한글은 24자에서 넘는다)
- 해싱(회원가입, 로그인, 비밀번호 변경, 탈퇴 재확인)은 동시에 N개(초기값 CPU 코어 수)까지만 처리한다. 최대 1초 기다려도 자리가 없으면 `503 SERVICE_BUSY` + `Retry-After: 1`
- 로그인·회원가입 요청 제한은 core가 직접 한다. 넘으면 `429 TOO_MANY_REQUESTS`

| 기준 | 목적 | 한도 |
| --- | --- | --- |
| 계정(이메일)별 실패 횟수 | 한 계정 비밀번호 대입 | 엄격. 넘으면 점점 늘어나는 지연 |
| IP별 요청 수 | 한 곳에서 여러 계정 대입 | 느슨하게 (공유 IP 때문) |

- 앞에 프록시가 없으므로 IP는 연결의 remote address를 쓰고 `X-Forwarded-For`는 믿지 않는다. IPv6는 /64 단위로 센다
- 한도 값은 구현 때 정한다

## 권한 모델

| 층 | 저장 | 값 | 누가 판단 |
| --- | --- | --- | --- |
| 계정 역할 | `member`, JWT `roles` | `MEMBER`, `ADMIN` | core (액세스 토큰으로) |
| 계정 상태 | `member.status` | `ACTIVE`, `RESTRICTED`(노쇼 2회), `WITHDRAWN` | core (예약할 때) |
| 방 역할 | `reservation.role` | `INTERVIEWER`, `INTERVIEWEE` | realtime (방 안), core (방 밖) |

realtime이 방 안에서 판단하는 것 (예시):

| 행동 | 면접관 | 면접자 |
| --- | --- | --- |
| 질문 진행·넘기기 | O | X |
| 답변 (발화) | O | O |
| 피드백 주기 | O (키워드·한 줄 평) | O (좋아요만) |
| 녹음 여부 선택 | X | O (본인) |

- 시간 조건(시작 1시간 전부터 면접관만 자소서 열람·커스텀 질문 작성)은 core REST API에서 판단한다
- 한 방 안에서 역할은 예약 때 고른 하나로 고정된다

## API

**REST** (core)

요청·응답 형식, 쿠키, 에러 코드는 [인증/인가 API 명세](https://app.notion.com/p/depromeet/API-42145b4338b380529a21fd05b742576b?source=copy_link)에서만 관리한다. 에러는 Problem JSON(RFC 9457) + `code`(UPPER_SNAKE)이고, 액세스 토큰 검증 실패 401에는 `WWW-Authenticate: Bearer error="invalid_token"`을 붙인다.

| Method | URL | 설명 |
| --- | --- | --- |
| POST | `/api/v1/auth/signup` | 회원가입 (가입 직후 로그인 상태) |
| POST | `/api/v1/auth/login` | 로그인 |
| POST | `/api/v1/auth/reissue` | 액세스 토큰 재발급 |
| POST | `/api/v1/auth/logout` | 로그아웃 |
| POST | `/api/v1/auth/ws-ticket` | WebSocket 티켓 발급 |
| GET | `/.well-known/jwks.json` | 공개키 목록 (realtime이 호출) |

**gRPC** (realtime → core)

- `GetParticipants`: [`proto/`](../../proto/)

**WebSocket** (브라우저 → realtime)

- 연결: `/ws?ticket=티켓`
- 연결 후 오류 종료 코드: `4001 INVALID_TOKEN`, `4002 TOKEN_EXPIRED`, `4003 FORBIDDEN`

## 관련 결정

- [인증에 JWT를 쓰고 각 서버가 직접 검증한다](../adr/auth-use-jwt.md)
- [토큰은 RS256으로 서명한다](../adr/auth-sign-with-rs256.md)
- [토큰 형식과 검증 규칙은 RFC 9068과 RFC 8725를 따른다](../adr/auth-follow-rfc9068.md)
- [리프레시 토큰은 DB에 저장하는 랜덤 문자열로 만들고 쓸 때마다 교체한다](../adr/auth-opaque-refresh-token.md)
- [로그아웃은 리프레시 토큰만 폐기한다](../adr/auth-logout-without-blacklist.md)
- [방 역할은 토큰에 넣지 않고 예약 데이터에서 받는다](../adr/auth-room-role-from-reservation.md)
- [WebSocket은 일회용 티켓으로 인증한다](../adr/auth-websocket-ticket.md)
- [게스트 토큰을 발급하지 않고 로비는 공개 조회로 연다](../adr/auth-remove-guest-token.md)
- [realtime → core gRPC는 공유 랜덤 토큰으로 인증한다](../adr/auth-grpc-shared-token.md)
- [서명 키는 Secrets Manager에 두고 메모리에만 올린다](../adr/auth-signing-key-in-secrets-manager.md)
- [게이트웨이 없이 core가 직접 요청 제한을 한다](../adr/auth-no-gateway.md)
- [비밀번호 해시 앞에 알고리즘 이름표를 붙인다](../adr/auth-prefix-password-hash.md)
- [비밀번호 해싱은 동시에 N개까지만 처리한다](../adr/auth-limit-hashing-concurrency.md)
