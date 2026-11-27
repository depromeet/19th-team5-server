# 발화는 서버가 아니라 받는 클라이언트가 판정한다

승인 | 2026-11-27 | 관련 RFC: 없음

## 상황

클라이언트는 누가 말하고 있는지 보여 주려 한다. 처음 설계는 서버가 패킷 헤더의 RFC 6464 음량 값을 읽어 발화를 판정하고, `voice.speaking`으로 방에 알리는 것이었다. 이 설계를 고른 이유는 남아 있지 않다. 발화 감지를 구현하기 전에 누가 판정할지 다시 정해야 했다.

## 결정

- 서버는 offer에 RFC 6464 오디오 레벨 확장을 넣고, 클라이언트가 채운 값을 고치지 않고 패킷과 함께 넘긴다.
- 발화는 받는 클라이언트가 `RTCRtpReceiver.getSynchronizationSources()`의 `audioLevel`로 판정한다. 판정 규칙(시작 100ms, 종료 500ms)은 [voice.md](../design/voice.md)의 "화자 식별과 발화 감지"를 따른다.
- 서버는 발화를 판정하지 않고 `voice.speaking`을 보내지 않는다.

## 이유

**받는 쪽이 이미 그 값을 갖고 있다.**

- 서버는 받은 패킷의 헤더 확장을 그대로 넘긴다. Pion v4.2.29 `track_local_static.go`의 `writeRTP`는 헤더를 복제해 SSRC와 payload type만 바꾼다.
- 넘긴 값의 확장 ID가 받는 쪽 협상과 맞는다.
  - 서버가 모든 offer를 만들고, Pion은 offer를 만들 때 `MediaEngine`에 등록한 순서대로 비어 있는 가장 작은 ID를 준다(`mediaengine.go`의 `getRTPParametersByKind`). 연결마다 같은 등록을 쓰므로 ID가 같다.
  - 답하는 쪽은 offer의 ID를 그대로 써야 한다. RFC 8285 7절: "the same identifier MUST be used for the same offered extension in the answer". 재협상에서도 "Identifier values in the valid range MUST NOT be altered (remapped)".
  - Pion 가짜 클라이언트 두 개로, 한쪽이 보낸 음량 값을 다른 쪽이 같은 값으로 받는 것을 테스트로 확인했다.
- 브라우저는 그 값을 표준 API로 내준다. W3C webrtc-pc 편집자 초안의 `RTCRtpSynchronizationSource.audioLevel`: "For SSRCs, this MUST be converted from the level value defined in [[!RFC6464]]." `getSynchronizationSources()`는 "for each unique SSRC identifier received by this RTCRtpReceiver in the last 10 seconds"를 돌려준다.
- MDN browser-compat-data(`api/RTCRtpReceiver.json`)에 따르면 `getSynchronizationSources`는 Chrome 73, Firefox 59, Safari 12.1부터 지원하고 Edge와 iOS Safari는 각각 Chrome, Safari를 따른다.

받는 쪽이 표준 API로 얻을 수 있는 정보를 서버가 다시 계산해 보내지 않는다.

**버린 대안: 서버 판정**

- 판정 패키지, 연결마다 시간을 재는 타이머, 미디어 모듈에서 시그널링으로 가는 발화 알림, `voice.speaking` 메시지와 방 전파가 필요하다.
- 모든 참가자가 정확히 같은 결과를 본다는 장점이 있다. 클라이언트 판정도 같은 값에 같은 규칙을 쓰므로 결과가 거의 같고, 값을 읽는 주기만큼만 어긋날 수 있다.
- 참고로 LiveKit은 서버에서 판정한다(`pkg/sfu/audio/audiolevel.go`의 `DefaultAudioLevelConfig`: `ActiveLevel: 35`, `MinPercentile: 40`, `UpdateInterval: 400`).

## 결과

- 얻는 것: 서버에 판정 코드, 타이머, 메시지가 없다. 음소거한 참가자는 서버가 패킷을 넘기지 않으므로 따로 처리하지 않아도 클라이언트에서 발화 종료가 된다.
- 감수하는 것:
  - 참가자마다 발화 표시가 `audioLevel`을 읽는 주기만큼 어긋날 수 있다.
  - 연결마다 확장 ID가 같은 것은 Pion이 문서로 약속하지 않은 구현 동작이다. `RegisterHeaderExtension`의 문서는 협상이 끝난 뒤 ID를 확인하라고만 한다. Pion을 올릴 때 이 동작이 바뀌면 전달 테스트가 실패하고, 그때는 서버가 넘기는 패킷마다 받는 쪽 ID로 바꿔 쓴다.
  - 서버는 누가 언제 말했는지 모른다. 발화 시간 기록처럼 서버가 발화 정보를 써야 하는 기능이 생기면 서버 판정을 더한다. 확장 협상은 서버 판정에도 그대로 필요하므로 되돌릴 것이 없다.
  - Firefox와 Safari가 보내는 패킷에 RFC 6464 확장을 싣는지는 확인하지 않았다. 싣지 않으면 그 브라우저 사용자의 발화는 알 수 없고, 서버 판정이어도 같다. 브라우저 시험에서 확인한다.
