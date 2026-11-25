# docs

확정된 설계와 기술 결정을 코드와 같은 레포에서 관리합니다.

| 폴더 | 무엇을 | 답하는 질문 |
| --- | --- | --- |
| [`design/`](design/) | 현재 시스템 설계. 계속 갱신 | 지금 어떻게 돼 있지? |
| [`rfc/`](rfc/) | 변경 제안. 큰 변경을 구현 전에 리뷰받는 용도 | 이렇게 바꿔도 될까? |
| [`adr/`](adr/) | 결정 근거. 한 번 쓰면 고치지 않음 | 왜 이렇게 됐지? |

## 흐름

```
1. design/ 에 현재 설계가 있다
        │
2. rfc/ 에 변경을 제안한다 (PR로 리뷰)
        │
        ├─ 거절 → RFC 상태를 "거절"로 남기고 끝
        │
3. 승인 → adr/ 에 결정을 남기고, design/ 문서를 고친다
```

## 언제 무엇을 쓰나

- **design만 고치기**: 작은 수정 (예: 비밀번호 최소 길이 8자 → 10자)
- **RFC부터 쓰기**: 다른 사람이 "이거 왜 바뀌었지?" 하고 물어볼 만한 변경
  - 여러 서버(core ↔ realtime)에 걸친 변경
  - `proto/` 계약 변경, DB 스키마의 큰 변경
  - 새 인프라·라이브러리 도입, 되돌리기 어려운 결정
- **ADR만 쓰기**: RFC 없이 정한 결정이라도 이유를 남길 만하면 (예: 초기 기술 스택 선택)

## 파일 이름

서버(core / realtime)가 아니라 **도메인**으로 나눈다. 기능 하나가 두 서버에 걸쳐 있어도 한 파일에서 흐름을 본다.

| 폴더 | 이름 규칙 | 예시 |
| --- | --- | --- |
| `design/` | `도메인.md` | `auth.md`, `reservation.md` |
| `rfc/` | `NNNN-도메인-바꾸려는것.md` | `0001-auth-websocket-ticket.md` |
| `adr/` | `NNNN-도메인-정한것.md` | `0001-auth-use-jwt.md` |

- **번호:** RFC와 ADR에만 4자리 번호를 붙인다. design은 도메인마다 하나뿐이고 계속 고쳐 쓰므로 붙이지 않는다.
  - 번호는 PR을 올릴 때 그 폴더의 다음 번호로 정한다. RFC와 ADR은 따로 센다.
  - 본문에서는 `RFC-0003`, `ADR-0003`처럼 종류를 붙여 부른다.
  - 거절된 RFC와 대체된 ADR도 번호를 그대로 두고, 그 번호를 다시 쓰지 않는다.
  - 열린 PR 두 개가 같은 번호를 쓰면, 나중에 머지하는 쪽이 번호를 바꾼다.
- **도메인:** `design/`의 파일 이름과 같게 쓴다(`auth.md`의 결정은 `NNNN-auth-...`). design 문서가 없는 주제를 결정할 때는 design 문서를 함께 만든다. 두 서버에 걸치는 주제(배포, 로깅, 공통 에러 형식 등)도 마찬가지다(예: `infra.md`, `observability.md`). 한 서버에만 해당하는 기술 결정은 서버 이름을 쓴다(예: `0002-realtime-use-gorilla-websocket.md`).
- **형식:** 영문 kebab-case로 쓴다. ADR은 정한 내용을 동사구로(`use-jwt`), RFC는 바꾸려는 것을(`websocket-ticket`) 쓴다.
- RFC와 그 결과 ADR은 번호가 달라도 된다. 짝은 ADR 첫 줄의 "관련 RFC" 링크로 잇는다.

## 문서 제목

| 폴더 | 제목 | 예시 |
| --- | --- | --- |
| `design/` | 도메인 이름 | 인증/인가 설계 |
| `rfc/` | 무엇을 바꾸는지 | WebSocket 인증을 티켓 방식으로 변경 |
| `adr/` | 정한 내용을 평서문으로 | 인증에 JWT를 사용한다 |

제목은 한국어로 쓰고 번호는 넣지 않는다. 번호는 파일 이름에만 둔다.

각 폴더의 `_template.md`를 복사해서 시작한다.

## 템플릿 참고 자료

| 템플릿 | 참고 | 가져온 것 |
| --- | --- | --- |
| design | [Design Docs at Google](https://www.industrialempathy.com/posts/design-docs-at-google/) | 목표와 비목표 |
| rfc | [Rust RFC 템플릿](https://github.com/rust-lang/rfcs/blob/master/0000-template.md) | 단점, 미해결 질문 |
| adr | [Documenting Architecture Decisions (Michael Nygard)](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions), [MADR](https://adr.github.io/madr/) | 상황 / 결정 / 결과 |

## 무엇을 어디에

| 무엇 | 어디 |
| --- | --- |
| 기능 설계, 서버 간 흐름 | `docs/design/` |
| 큰 변경 제안·결정 | `docs/rfc/`, `docs/adr/` |
| 서버 간 gRPC 계약 | [`proto/`](../proto/) |
| 패키지 구조, 실행 방법, 로컬 설정 | `services/core/README.md`, `services/realtime/README.md` |
