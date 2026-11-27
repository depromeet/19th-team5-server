// Command realtime runs the interview room WebSocket and voice SFU server.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/room"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media/pion"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/signaling"
)

func main() {
	httpAddr := flag.String("http", "127.0.0.1:8080", "HTTP listen address")
	voiceUDPAddr := flag.String("voice_udp", "127.0.0.1:50000", "voice UDP listen address")
	originsFlag := flag.String("origins", "", "comma-separated allowed WebSocket origin patterns")
	dev := flag.Bool("dev", false, "use development ticket verifier and fixed participant list")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if !*dev {
		logger.Error("core ticket verifier and participant store are not implemented yet; pass -dev for local development")
		os.Exit(1)
	}

	var originPatterns []string
	if *originsFlag != "" {
		for p := range strings.SplitSeq(*originsFlag, ",") {
			if p = strings.TrimSpace(p); p != "" {
				originPatterns = append(originPatterns, p)
			}
		}
	}

	if err := run(logger, *httpAddr, *voiceUDPAddr, originPatterns); err != nil {
		logger.Error("realtime server failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, httpAddr, voiceUDPAddr string, originPatterns []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var lc net.ListenConfig
	udpConn, err := lc.ListenPacket(ctx, "udp4", voiceUDPAddr)
	if err != nil {
		return err
	}
	driver := pion.NewDriver(udpConn)
	voiceSrv := signaling.NewServer(driver, logger)

	roomSrv := room.NewServer(devTicketVerifier{}, devParticipantStore{}, room.Options{
		Logger:         logger,
		OriginPatterns: originPatterns,
		NewVoiceRoom: func(send func(ctx context.Context, participantID string, msg []byte)) room.VoiceRoom {
			return voiceSrv.NewRoom(send)
		},
	})

	mux := http.NewServeMux()
	mux.Handle("/ws", roomSrv)
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("realtime server listening", "http", httpAddr, "voiceUdp", udpConn.LocalAddr().String(), "dev", true)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	var serveErr error
	select {
	case serveErr = <-errCh:
	case <-ctx.Done():
		logger.Info("shutting down realtime server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("HTTP server shutdown failed", "err", err)
	}
	if err := roomSrv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("room server shutdown failed", "err", err)
	}
	if err := driver.Close(); err != nil {
		logger.Warn("voice driver close failed", "err", err)
	}
	return serveErr
}

// devTicketVerifier treats the ticket query parameter itself as the member ID.
type devTicketVerifier struct{}

func (devTicketVerifier) Verify(_ context.Context, ticket string) (string, error) {
	return ticket, nil
}

// devParticipantStore returns four fixed participants for any interview ID.
type devParticipantStore struct{}

func (devParticipantStore) Participants(_ context.Context, _ string) ([]room.Member, error) {
	return []room.Member{
		{MemberID: "m1", Nickname: "면접관1", Role: room.RoleInterviewer},
		{MemberID: "m2", Nickname: "면접관2", Role: room.RoleInterviewer},
		{MemberID: "m3", Nickname: "면접자1", Role: room.RoleInterviewee},
		{MemberID: "m4", Nickname: "면접자2", Role: room.RoleInterviewee},
	}, nil
}
