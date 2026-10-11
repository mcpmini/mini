package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/mcpmini/mini/internal/daemon"
)

type socketBindParams struct {
	Socket string
	Chmod  func(string, os.FileMode) error
}

func bindSocket(p socketBindParams) (net.Listener, error) {
	dir := filepath.Dir(p.Socket)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	// The dir's permissions are the access boundary — macOS ignores the socket file's own mode on connect.
	if err := p.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("set socket dir permissions: %w", err)
	}
	ln, err := listenDaemonSocket(p.Socket)
	if err != nil || ln == nil {
		return ln, err
	}
	// Linux honors the socket file's own mode on connect; a permissive umask would otherwise leave it world-writable.
	if err := p.Chmod(p.Socket, 0o600); err != nil {
		permissionErr := fmt.Errorf("set socket permissions: %w", err)
		if closeErr := ln.Close(); closeErr != nil {
			return nil, errors.Join(permissionErr, fmt.Errorf("close unsafe socket listener: %w", closeErr))
		}
		return nil, permissionErr
	}
	return ln, nil
}

func listenDaemonSocket(socket string) (net.Listener, error) {
	ln, err := net.Listen("unix", socket)
	if err == nil {
		return ln, nil
	}
	if daemon.SocketHealthy(socket) {
		return nil, nil
	}
	if removeErr := os.Remove(socket); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket %s: %w", socket, removeErr)
	}
	ln, err = net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("bind socket %s: %w", socket, err)
	}
	return ln, nil
}
