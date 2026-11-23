# Contributing

팀 작업 규칙입니다. 규칙이 바뀌면 이 파일을 PR로 고칩니다.

작업 흐름: **이슈 생성 → 브랜치 생성 → 커밋 → PR → 리뷰 → 머지 → 브랜치 삭제**

## 브랜치

```
main      ← 운영 배포 (태그: v1.0.0)
 ├ hotfix/#45-login-crash    ← 운영 긴급 수정
develop   ← 개발 서버 배포, 기능이 모이는 곳
 ├ feat/#12-social-login     ← 기능 작업
 ├ fix/#31-jwt-expire        ← 개발 중 버그
 └ release/v1.0.0            ← 배포 준비 (QA, 버전 올리기)
```

| 브랜치 | 어디서 따나 | 어디로 머지 | 머지 방식 |
| --- | --- | --- | --- |
| `feat/*` `fix/*` `refactor/*` | `develop` | `develop` | Squash merge |
| `release/vX.Y.Z` | `develop` | `main` + `develop` | Merge commit |
| `hotfix/*` | `main` | `main` + `develop` | Merge commit |

- 브랜치 이름: `타입/#이슈번호-설명` (예: `feat/#12-social-login`). release만 `release/v1.0.0`
- `fix/*`는 개발 중 버그, 운영 장애는 `hotfix/*`
- 초기(유저 없을 때)에는 release를 생략하고 `develop` → `main` PR로 바로 배포해도 된다

### 브랜치 보호 규칙

- `main`: 직접 push 금지. release·hotfix PR만 받는다
- `develop`: 직접 push 금지. PR로만 머지하고, Approve 없이 작성자가 머지한다
- Default branch는 `develop`

## 커밋

```
<type>: <제목>

<본문: 무엇을, 왜>   ← 선택
```

예: `feat: PIN 입력으로 세션 입장`

| type | 언제 |
| --- | --- |
| `feat` | 새 기능 |
| `fix` | 버그 수정 |
| `refactor` | 동작 그대로, 구조만 개선 |
| `style` | 포맷, 이름 정리 |
| `ui` | 화면, 디자인 수정 |
| `test` | 테스트 |
| `docs` | 문서, 주석 |
| `chore` | 빌드·프로젝트 설정 |
| `deps` | 패키지 추가 / 제거 |
| `perf` | 성능 개선 |
| `schema` | 데이터 계약 변경 |
| `security` | 보안 규칙, 권한 |
| `asset` | 이미지, 도면, Fixtures |
| `hotfix` | 급한 수정 |
| `revert` | 되돌리기 |
| `init` | 프로젝트 시작 |

### 규칙

1. 제목은 한국어, 50자 안쪽, 마침표 없이 쓴다
2. 한 커밋에 한 가지 일만 담는다
3. `schema` 커밋은 본문 필수: 바꾼 필드, 이유, schemaVersion 변화
4. 이모지는 쓰지 않는다

## 이슈

`.github/ISSUE_TEMPLATE/`의 템플릿으로 연다.

| 템플릿 | 언제 |
| --- | --- |
| ✨ 기능 | 새 기능 추가 |
| 🐞 버그 | 버그 제보 |
| ♻️ 리팩토링 | 동작 변경 없이 구조 개선 |

이슈 번호는 브랜치 이름(`feat/#12-...`)과 PR(`close: #12`)에 그대로 쓴다.

## PR

`.github/PULL_REQUEST_TEMPLATE.md`를 채운다. 관련 이슈는 `close: #번호`로 연결한다.

### 리뷰 · 머지 규칙

1. 가능하다면 변경 파일은 20개 이하로 유지한다
2. 머지는 PR 작성자가 직접 진행한다
3. `develop`으로 머지할 때는 Squash merge를 사용한다
4. `main`으로 머지할 때는 Merge commit을 사용한다
5. PR 머지 후 작업 브랜치는 삭제한다
6. 코드 리뷰는 `develop` → `main` PR에서 한다. `develop`으로 가는 기능 PR은 리뷰 없이 머지한다

### 리뷰는 무엇을 보나

`develop` → `main` 리뷰는 필드명·코드 스타일 같은 세부가 아니라 **왜 그 방법을 선택했는지**에 집중한다.

- 이 구조·기술을 고른 이유와 버린 대안
- 서버 간(core ↔ realtime) 경계, `proto/` 계약, DB 설계에 미치는 영향
- 나중에 되돌리기 어려운 결정인지

리뷰에서 나온 큰 결정은 [ADR](docs/adr/)로 남긴다.

Squash merge하면 PR 제목이 `develop`의 커밋 메시지가 되므로, **PR 제목도 커밋 컨벤션**(`feat: ...`)을 따른다.

### RCA 룰

리뷰 코멘트 앞에 강도를 붙인다.

- **R**: 꼭 반영해 주세요. 적극적으로 고려해 주세요. (Request changes)
- **C**: 웬만하면 반영해 주세요. (Comment)
- **A**: 반영해도 좋고 넘어가도 좋습니다. 그냥 사소한 의견입니다. (Approve)

## 설계 문서

확정된 설계와 기술 결정은 이 레포의 [`docs/`](docs/)에서 관리한다.

| 폴더 | 무엇을 |
| --- | --- |
| `docs/design/` | 현재 시스템 설계 (계속 갱신) |
| `docs/rfc/` | 큰 변경의 사전 리뷰용 제안 |
| `docs/adr/` | 결정 근거 기록 |

흐름: **설계 문서 → RFC로 변경 제안 → 승인되면 ADR 작성 + 설계 문서 갱신.** 언제 RFC를 쓰는지 등 자세한 기준은 [docs/README.md](docs/README.md).
