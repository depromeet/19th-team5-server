# 음성 시그널링은 서버만 offer를 만들고 W3C 이름과 AIP-193 에러 형식을 쓴다

승인 | YYYY-MM-DD | 관련 RFC: 없음

## 상황

WebRTC 표준은 offer와 answer를 주고받는 시그널링 메시지 형식을 정하지 않으므로 직접 정해야 한다.
- 업계 SFU(LiveKit, Janus 등)도 자체 형식을 쓴다.
- SIP, XMPP Jingle, WHIP 같은 시그널링 표준은 서버가 먼저 재협상하고 음소거와 발화 상태를 알리는 이 서버의 흐름을 담지 못하거나, 프로토콜 전체를 들여와야 한다.
- [트랙이 바뀔 때마다 재협상](0017-voice-renegotiate-single-connection.md)하므로 offer와 answer가 자주 오간다. 양쪽이 동시에 offer를 보내면 협상이 엉키고(glare), answer가 늦거나 사라져 offer를 다시 보내면 늦게 온 이전 answer와 새 offer가 엇갈릴 수 있다.

## 결정

- offer는 항상 서버만 만들고 클라이언트는 answer만 보낸다. ICE 재시작이 필요하면 클라이언트가 서버에 요청한다.
- 참가자마다 offer에 1부터 1씩 늘어나는 `offerId`를 붙이고, 클라이언트는 answer에 그 값을 그대로 돌려준다. 서버는 마지막으로 보낸 offer와 `offerId`가 다른 answer를 버린다.
- answer가 10초 안에 오지 않거나 적용에 실패하면 offer를 다시 보내지 않는다. 연결을 닫고 `voice.error`의 `CONNECTION_FAILED`로 알리며, 클라이언트는 `RTCPeerConnection`을 새로 만들고 `voice.join`을 보낸다. 연결 열기나 offer 생성에 실패해도 같다.
- 서버는 ICE 후보를 offer의 SDP에 모두 담고 `voice.candidate`로 따로 보내지 않는다.
- SDP, ICE 후보, ICE 서버 필드는 W3C WebRTC API의 이름과 모양을 그대로 쓴다.
- 필드 이름은 대소문자를 구분하고, 같은 이름이 두 번 나오거나 UTF-8이 잘못된 메시지는 거부한다. 모르는 필드는 무시한다.
- `payload`를 생략하거나 `null`로 보내면 빈 객체로 보고, 객체가 아닌 `payload`는 거부한다.
- `candidate`는 필수 필드다. `candidate`가 빈 문자열이 아닌데 `sdpMid`와 `sdpMLineIndex`가 둘 다 `null`인 후보는 거부한다.
- 에러는 AIP-193의 `ErrorInfo.reason`(UPPER_SNAKE_CASE), `ErrorInfo.metadata`, `Status.message`를 쓴다. `message`는 원인마다 고정된 문구에 해결 방법을 붙이고, 원인(`cause`)과 잘못된 필드처럼 요청마다 달라지는 값은 `metadata`에 넣는다. 같은 reason이면 `metadata`의 키도 늘 같다. canonical 상태 코드와 `domain`은 두지 않는다.
- 면접 전 점검 결과는 클라이언트에 메시지로 알리지 않는다.

## 이유

- **서버만 offer:** v1에서는 클라이언트가 먼저 트랙을 추가할 일이 없으므로, 서버만 offer를 만들면 glare가 생기지 않는다.
- **`offerId`:**
  - WebSocket이 잠깐 끊긴 사이 offer나 answer가 사라지면, 다시 보내지 않는 한 그 참가자의 협상이 멈춘다. 그래서 WebSocket이 다시 붙으면 대기 중인 offer를 새 `offerId`로 다시 보낸다(실시간 서버와 연결할 때 만든다). WebSocket이 살아 있는 동안에는 메시지가 사라지지 않으므로 그 밖에는 다시 보내지 않는다.
  - 다시 보낸 뒤에 이전 offer의 answer가 도착할 수 있다. answer의 SDP에는 어느 offer에 대한 응답인지가 들어 있지 않다.
  - 시그널링을 정의하는 SIP는 요청과 응답을 `CSeq` 번호로 짝짓고, LiveKit도 offer와 answer에 id를 붙여 짝짓는다.
- **실패하면 다시 시도하지 않고 클라이언트가 다시 들어온다:**
  - Pion은 offer를 보낸 상태(`have-local-offer`)에서 새 offer를 설정하지도, rollback하지도 못한다. v4.2.29 `signalingstate.go`의 `checkNextSignalingState`는 이 상태에서 원격 answer(와 pranswer) 설정만 받는다. 그래서 answer가 오지 않는 한 같은 연결로 offer를 다시 보낼 수 없다.
  - 서버만 연결을 새로 만들면 클라이언트는 이전 연결에 새 offer를 적용하게 되어 따라올 수 없다. 클라이언트도 연결을 새로 만들어야 하므로 클라이언트가 다시 들어오는 흐름 하나로 모은다.
  - LiveKit도 협상 타임아웃과 적용 실패에 클라이언트 전체 재접속을 시킨다.
- **실패 reason 하나:** 연결 열기, offer 생성, answer 적용, 타임아웃 어느 원인이든 클라이언트가 할 일은 "연결을 새로 만들고 `voice.join`"으로 같다. AIP-193 `ErrorInfo`는 같은 에러에 같은 reason을 쓰라고 하고(must), 같은 에러인지는 "the expected action a client might take to resolve them"으로 판단하라고 한다(should). 원인은 `metadata`의 `cause`가 구분한다. AIP-193은 메시지에서 달라지는 부분을 모두 metadata에 넣게 해(must), 클라이언트가 문구를 파싱하지 않아도 되게 한다.
- **서버 ICE 후보를 offer에 담기:** 서버의 후보는 UDP mux가 받는 주소 하나에서 나오고, STUN으로 찾을 주소가 없어 offer를 만들 때 바로 모인다. TURN 중계 후보는 클라이언트 쪽에서 생긴다. 따로 보낼 후보가 없으므로 서버에서 후보를 보내는 코드와 그 메시지의 `usernameFragment`를 어떻게 채울지 정할 일이 없어진다.
- **W3C 이름:** 클라이언트가 브라우저의 `RTCSessionDescription`, `RTCIceCandidate`, `RTCIceServer` 객체를 바꾸지 않고 보내고 받는다. Pion의 같은 타입과도 JSON 이름이 같다.
- **엄격하게 읽기:** 계약에 없는 형식을 받아 주지 않는다. Go 1.27 `encoding/json/v2`의 기본 동작이다. 모르는 필드를 무시하는 것은 AIP-180의 하위 호환 원칙으로, 나중에 필드를 추가해도 이전 버전이 그대로 동작한다.
- **payload 생략과 `null` 허용:** 빈 객체와 뜻이 같아 받아 줘도 놓치는 잘못이 없다. payload가 있는 메시지는 필수 필드 누락으로 걸리고, 없는 메시지는 확인할 내용이 없다. 대신 클라이언트가 빈 payload를 매번 채우지 않아도 되고, 나중에 payload가 없던 메시지에 선택 필드를 더해도 이전 클라이언트가 그대로 동작한다. 배열이나 문자열 같은 값은 payload를 문자열로 한 번 더 감싸는 이중 인코딩 같은 클라이언트 버그라서 거부한다.
- **`sdpMid`와 `sdpMLineIndex`가 둘 다 `null`인 후보 거부:** W3C WebRTC(2025-03-13 권고안) 4.4.2 `addIceCandidate` 3단계는 `candidate`가 빈 문자열이 아니고 `sdpMid`와 `sdpMLineIndex`가 둘 다 `null`이면 `TypeError`로 거절한다. 브라우저가 받지 않는 후보를 서버도 받지 않는다.
- **`candidate` 필수:** W3C `RTCIceCandidateInit`은 `candidate`가 없으면 빈 문자열(수집 끝)로 본다. 그런데 대소문자 구분과 모르는 필드 무시 때문에, 필드 이름을 잘못 쓴 후보는 필드가 없는 것으로 읽힌다. 이를 빈 문자열로 보면 실제 후보가 수집 끝으로 처리되어 버려지고 클라이언트는 오류를 받지 못한다.
- **AIP-193의 reason과 message:** 클라이언트는 고정된 `reason`으로 분기하고, `message`는 디버깅용이라 바뀌어도 된다.
- **봉투 필드 이름 `type`:** AIP-140은 흔한 프로그래밍 언어의 키워드와 겹치는 필드 이름을 피하라고 하고(should), AIP-190은 그런 이름을 쓸 수는 있지만 리뷰에서 더 따져 보라고 한다(may). `type`은 Go, Rust, TypeScript의 키워드다. 하지만 W3C `RTCSessionDescriptionInit`도 SDP 종류를 `type` 필드로 나타내서 클라이언트에 익숙한 이름이고, 메시지 종류를 가르는 뜻도 분명하다.
- **상태 코드와 domain을 두지 않는 이유:**
  - 상태 코드는 HTTP나 gRPC 응답에서 중간 계층이 해석하는 값인데, 이 메시지는 WebSocket 안에서 오간다.
  - HTTP 상태 코드는 WebSocket 업그레이드 응답에만 쓰인다.
  - WebSocket 종료 코드는 연결 전체를 닫을 때 쓰는 값이라 요청 하나의 실패에 쓸 수 없다.
  - 에러를 내는 서비스가 하나라 `domain`도 필요 없다.
- **점검 결과를 알리지 않는 것:** 점검 연결의 성공 여부와 경로는 클라이언트가 연결 상태와 `getStats()`로 이미 안다.

## 결과

- 얻는 것: glare가 없고, 늦은 answer를 안전하게 처리한다. 협상 실패의 복구 흐름이 "다시 들어오기" 하나다. 클라이언트에 변환 코드가 필요 없다. 필드를 추가해도 하위 호환된다. 오타가 조용히 묻히지 않는다.
- 감수하는 것: 클라이언트가 먼저 협상을 시작할 수 없다. answer가 늦거나 적용에 실패하면 클라이언트가 다시 들어와야 하고, 그동안 음성이 끊긴다. 10초는 측정 근거가 없는 초기값이다(LiveKit은 15초). W3C보다 엄격한 지점(`candidate` 필수)을 클라이언트가 알아야 한다. Rust처럼 `type`이 키워드인 언어의 클라이언트는 필드 이름을 바꿔 읽어야 한다. 실시간 서버의 다른 메시지도 같은 에러 형식을 쓰는 것이 좋다. 이건 실시간 서버 설계에서 정한다.
