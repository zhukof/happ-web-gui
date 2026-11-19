package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

func serveProxy(ctx context.Context, listener net.Listener, destination string) error {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	log.Printf("TCP forwarding %s -> %s", listener.Addr(), destination)

	for {
		client, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept proxy connection on %s: %w", listener.Addr(), err)
		}
		go forwardTCP(ctx, client, destination)
	}
}

func forwardTCP(ctx context.Context, client net.Conn, destination string) {
	defer client.Close()
	dialer := net.Dialer{Timeout: 5 * time.Second}
	backend, err := dialer.DialContext(ctx, "tcp", destination)
	if err != nil {
		return
	}
	defer backend.Close()

	stop := context.AfterFunc(ctx, func() {
		_ = client.Close()
		_ = backend.Close()
	})
	defer stop()

	done := make(chan struct{})
	go func() {
		copyTCP(backend, client)
		close(done)
	}()
	copyTCP(client, backend)
	<-done
}

func copyTCP(dst, src net.Conn) {
	if _, err := io.Copy(dst, src); err == nil {
		if tcp, ok := dst.(*net.TCPConn); ok && tcp.CloseWrite() == nil {
			return
		}
	}
	_ = dst.Close()
	_ = src.Close()
}
