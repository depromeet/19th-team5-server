# realtime → core gRPC는 공유 랜덤 토큰으로 인증한다

승인 | 2026-11-24 | 관련 RFC: 없음

## 상황

realtime이 core에 `GetParticipants(interview_id)`로 참가자 목록(닉네임 포함)을 받는다. 확인이 없으면 방 번호만 알면 누구든 참가자 정보를 받아 갈 수 있다. 내부망이라도 서버 하나가 털리면 그 서버인 척 물어볼 수 있다.

## 결정

- v1은 두 서버만 아는 공유 랜덤 토큰으로 인증한다
  - 32바이트 랜덤 값 (`SecureRandom` 또는 `openssl rand`)
  - realtime은 gRPC 메타데이터에 실어 보내고, core는 `ServerInterceptor`에서 일정 시간 비교(`MessageDigest.isEqual`)로 확인한다
  - 교체할 때는 core가 새 토큰과 옛 토큰 두 개를 잠시 함께 받는다
- 인증과 별개로 core는 "이 방이 지금 조회할 수 있는 시간인가"를 확인한다
- 상태 코드: 토큰 없음·틀림은 `UNAUTHENTICATED`, 방이 시작 전이거나 끝났으면 `FAILED_PRECONDITION`
- 서버를 여러 대로 나눌 때 mTLS로 바꾼다

## 이유

- 서비스 JWT: 만료 시간을 넣을 수 있지만 서명 키를 하나 더 관리해야 한다. 지금 얻는 것에 비해 무겁다
- mTLS: 공유 비밀이 없고 통신도 암호화돼 가장 안전하다. 하지만 사설 CA와 인증서 발급·갱신을 운영해야 한다. 서버가 한 대인 v1에는 과하다
- 공유 토큰은 구현이 인터셉터 하나다. 토큰이 새더라도 시간 확인 때문에 지금 진행 중인 방만 볼 수 있다

## 결과

- 얻는 것: 적은 비용으로 "호출한 쪽이 realtime인가"를 확인한다
- 감수하는 것: 토큰에 만료가 없어서 직접 교체해야 한다. TLS 없이 보내면 네트워크에서 보일 수 있다. 서버 분리 전에 mTLS로 옮기는 새 ADR을 쓴다
- 확인할 것 (실험): 토큰 없음 / 틀린 토큰 / 시간 밖의 방에서 각각 `UNAUTHENTICATED`, `UNAUTHENTICATED`, `FAILED_PRECONDITION`이 오는지, 암호화 없는 gRPC를 Wireshark로 보면 토큰이 보이는지(TLS가 왜 필요한지 확인), 사설 CA로 mTLS 연결(서버 분리 전)
