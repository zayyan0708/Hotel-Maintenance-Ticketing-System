package mq

import (
	"errors"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	TopicTicketCreated       = "smarthotel/tickets/created"
	TopicTicketStatusUpdated = "smarthotel/tickets/status_updated"
	TopicTicketAssigned      = "smarthotel/tickets/assigned"
)

type Config struct {
	BrokerURL string
	ClientID  string
	Logger    *log.Logger
}

func Connect(cfg Config) (mqtt.Client, error) {
	if cfg.BrokerURL == "" {
		return nil, errors.New("MQTT broker URL is empty")
	}
	if cfg.ClientID == "" {
		cfg.ClientID = "smarthotel-client"
	}

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.BrokerURL).
		SetClientID(cfg.ClientID).
		SetConnectTimeout(5 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(2 * time.Second)

	if cfg.Logger != nil {
		opts.OnConnectionLost = func(_ mqtt.Client, err error) {
			cfg.Logger.Printf("mqtt connection lost: %v", err)
		}
		opts.OnConnect = func(_ mqtt.Client) {
			cfg.Logger.Printf("mqtt connected broker=%s client_id=%s", cfg.BrokerURL, cfg.ClientID)
		}
	}

	c := mqtt.NewClient(opts)
	tok := c.Connect()
	tok.Wait()
	if err := tok.Error(); err != nil {
		return nil, err
	}
	return c, nil
}
