package nats

import (
	"os"

	"github.com/nats-io/nats.go"
)

var Conn *nats.Conn

func Init() error {
	var err error
	Conn, err = nats.Connect(os.Getenv("NATS_URL"))
	if err != nil {
		return err
	}
	return nil
}

func Close() {
	if Conn != nil {
		Conn.Close()
	}
}

func Publish(subject string, data []byte) error {
	if Conn == nil {
		return nats.ErrConnectionClosed
	}
	return Conn.Publish(subject, data)
}

func Subscribe(subject string, handler nats.MsgHandler) (*nats.Subscription, error) {
	if Conn == nil {
		return nil, nats.ErrConnectionClosed
	}
	return Conn.Subscribe(subject, handler)
}
