//go:build integration

package testutil

import (
	"context"
	"net"

	"github.com/jackc/pgx/v5"
)

// ConnectWithDelayedTermination keeps the server session alive after pgx Close.
// Packet filtering requires the integration fixture's plaintext connection.
func ConnectWithDelayedTermination(ctx context.Context, url string) (*pgx.Conn, func(), error) {
	config, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, func() {}, err
	}
	var wires []net.Conn
	config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wires = append(wires, conn)
		return &delayedTermination{Conn: conn}, nil
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	return conn, func() {
		for _, wire := range wires {
			_ = wire.Close()
		}
	}, err
}

type delayedTermination struct{ net.Conn }

func (c *delayedTermination) Write(data []byte) (int, error) {
	if len(data) == 5 && data[0] == 'X' {
		return len(data), nil
	}
	return c.Conn.Write(data)
}

func (*delayedTermination) Close() error { return nil }
