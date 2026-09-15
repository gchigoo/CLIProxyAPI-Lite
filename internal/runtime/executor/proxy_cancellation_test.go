package executor

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

func TestProxyDialersCancelSOCKS5Negotiation(t *testing.T) {
	for _, transport := range []string{"http", "websocket"} {
		for _, scheme := range []string{"socks5", "socks5h"} {
			for _, phase := range []string{"method", "connect"} {
				t.Run(transport+"/"+scheme+"/"+phase, func(t *testing.T) {
					t.Parallel()
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					defer listener.Close()
					ready := make(chan net.Conn, 1)
					serverErr := make(chan error, 1)
					go func() {
						conn, errAccept := listener.Accept()
						if errAccept != nil {
							serverErr <- errAccept
							return
						}
						if errDeadline := conn.SetDeadline(time.Now().Add(5 * time.Second)); errDeadline != nil {
							conn.Close()
							serverErr <- errDeadline
							return
						}
						var greeting [3]byte
						_, errRead := io.ReadFull(conn, greeting[:])
						if errRead == nil && phase == "connect" {
							_, errRead = conn.Write([]byte{5, 0})
							if errRead == nil {
								var connect [10]byte
								_, errRead = io.ReadFull(conn, connect[:])
							}
						}
						if errRead != nil {
							conn.Close()
							serverErr <- errRead
							return
						}
						ready <- conn
					}()

					proxyURL := scheme + "://" + listener.Addr().String()
					var dialContext func(context.Context, string, string) (net.Conn, error)
					if transport == "websocket" {
						cfg := &config.Config{}
						cfg.ProxyURL = proxyURL
						dialer, errBuild := newProxyAwareWebsocketDialer(cfg, nil)
						if errBuild != nil {
							t.Fatal(errBuild)
						}
						dialContext = dialer.NetDialContext
					} else {
						rt, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
						if errBuild != nil {
							t.Fatal(errBuild)
						}
						defer rt.CloseIdleConnections()
						dialContext = rt.DialContext
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := make(chan error, 1)
					go func() {
						conn, errDial := dialContext(ctx, "tcp", "127.0.0.1:12345")
						if conn != nil {
							conn.Close()
						}
						done <- errDial
					}()
					var peer net.Conn
					select {
					case peer = <-ready:
					case errServer := <-serverErr:
						t.Fatal(errServer)
					case <-time.After(5 * time.Second):
						t.Fatal("SOCKS5 negotiation did not reach the expected phase")
					}
					defer peer.Close()
					cancel()
					select {
					case errDial := <-done:
						// The SOCKS5 dialer interrupts blocked IO by expiring its deadline.
						var netErr net.Error
						if !errors.Is(errDial, context.Canceled) && !(errors.As(errDial, &netErr) && netErr.Timeout()) {
							t.Fatalf("dial error = %v, want cancellation or interrupted IO", errDial)
						}
					case <-time.After(time.Second):
						t.Fatal("SOCKS5 negotiation ignored cancellation")
					}
					var data [1]byte
					if _, errRead := peer.Read(data[:]); errRead != io.EOF {
						t.Fatalf("canceled dial did not close its connection: %v", errRead)
					}
				})
			}
		}
	}
}
