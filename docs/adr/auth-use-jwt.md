# 인증에 JWT를 쓰고 각 서버가 직접 검증한다

승인 | 2026-11-24 | 관련 RFC: 없음

## 상황

토큰을 만드는 서버(core)와 토큰을 확인하는 서버(realtime)가 다르다. realtime은 WebSocket 연결을 받을 때마다 사용자를 확인해야 한다. 세션 방식이면 realtime이 확인할 때마다 core나 세션 저장소에 물어야 한다.

## 결정

액세스 토큰과 WebSocket 티켓은 JWT로 만든다. core와 realtime은 JWKS 공개키로 토큰을 직접 검증하고, 검증하려고 다른 서버에 묻지 않는다.

## 이유

- 세션: realtime이 매번 저장소를 조회해야 한다. 저장소(Redis 등)가 하나 더 생기고, 그 저장소가 멈추면 면접도 멈춘다
- realtime이 core에 토큰을 물어보기 (토큰 확인 API): core 장애가 진행 중인 면접으로 그대로 번진다
- JWT는 서명만 확인하면 되니 realtime이 혼자 판단할 수 있다
- 측정해 보니 RS256 검증은 Java 약 25µs, Go 약 23µs다 ([서명 알고리즘 결정](auth-sign-with-rs256.md)). DB 조회 한 번(보통 1ms 안팎)보다 훨씬 작아서 요청마다 검증해도 부담이 없다

## 결과

- 얻는 것: realtime이 core와 저장소 없이 인증한다. Redis 없이 시작할 수 있다
- 감수하는 것: 발급한 액세스 토큰을 만료 전에 취소할 수 없다 ([로그아웃 결정](auth-logout-without-blacklist.md)). 그래서 액세스 토큰은 30분, 티켓은 30초로 짧게 둔다
