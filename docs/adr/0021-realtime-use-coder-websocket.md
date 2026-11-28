# 실시간 서버 WebSocket은 coder/websocket을 쓴다

승인 | 2026-11-27 | 관련 RFC: 없음

## 상황

Go 1.27 표준 라이브러리에는 WebSocket 서버가 없다. 실시간 서버(`services/realtime/internal/room`)는 WebSocket 업그레이드, Origin 검사, 10초 주기 ping/pong, 메시지 크기 제한, 종료 코드(`1000`, `1001`, `1013`, `4003`, `4009`, `4012`) 전송, 서버 종료 시 연결 정리를 다뤄야 한다.

후보는 `github.com/coder/websocket` v1.8.15와 `github.com/gorilla/websocket` v1.5.3 둘이다.

## 결정

`github.com/coder/websocket`을 쓴다. 방 고루틴과 음성 시그널링의 비블로킹 송신 계약을 지키고 연결별 메시지 순서를 보장하기 위해, 연결마다 유한 버퍼 송신 큐와 쓰기 고루틴을 두고 종료 코드를 전달하는 `Close`는 비동기로, 느린 연결·ping 타임아웃은 `CloseNow`로 즉시 닫는다.

## 이유

- `coder/websocket`은 읽기, 쓰기, `Ping(ctx)`가 모두 `context.Context`를 받고, `Ping(ctx)`가 pong 응답까지 기다린다. `gorilla/websocket`은 데드라인, `WriteControl`, `SetPongHandler`를 직접 조합해야 한다.
- `Accept`가 기본으로 같은 호스트 Origin만 허용하고 `OriginPatterns`로 허용 목록을 더할 수 있으며, 기본 32768바이트 읽기 제한과 `StatusTryAgainLater` 같은 종료 코드 상수가 들어 있다.
- 외부 의존성이 없다.
- `coder/websocket`의 `Write`는 느린 소켓에서 블로킹되고 `Close`는 close 프레임 전송 5초 + 상대 응답 대기 5초로 최대 10초 블로킹되므로, 연결별 유한 버퍼 송신 큐·쓰기 고루틴과 비동기 `Close` / 즉시 `CloseNow`로 방 고루틴을 보호한다.

## 결과

- 얻는 것:
  - ping/pong, Origin 검사, 메시지 크기 제한, `context.Context` 취소를 적은 코드로 일관되게 다룬다.
  - 느린 연결이나 끊긴 연결이 방 고루틴을 막지 않는다.
- 감수하는 것:
  - 외부 모듈(`github.com/coder/websocket`) 하나가 `services/realtime/go.mod`에 추가된다.
  - `Accept` 반환 뒤에는 `http.Request.Context()`를 쓰지 않고 별도 연결 컨텍스트를 관리해야 하며, `http.Server.Shutdown`이 hijacked 연결을 닫지 않으므로 `room.Server.Shutdown(ctx)`로 따로 정리한다.
