# 19th-team5-server

디프만 19기 5팀 서버 레포지토리입니다.

## 폴더 구조

```
.
├── proto/              # 서비스 간 공용 gRPC 계약
└── services/
    ├── core/           # 핵심 API 서버
    └── realtime/       # 실시간 서버
```

## 통신 구조

- 클라이언트 → `core`: 핵심 API
- 클라이언트 → `realtime`: 실시간 통신
- `realtime` → `core`: gRPC (계약은 `proto/`에서 관리)

## 설계 문서

- 추후 작성 (노션 링크)
