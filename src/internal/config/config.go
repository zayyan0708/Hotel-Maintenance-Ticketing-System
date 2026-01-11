package config

import "os"

type GatewayConfig struct {
	Addr            string
	DBPath          string
	MQTTBroker      string
	MQTTClientID    string
	AuthServiceURL  string
	AuthInternalKey string
}

type AuthConfig struct {
	Addr           string
	DBPath         string
	InternalKey    string
	BootstrapAdmin bool
	BootstrapUser  string
	BootstrapPass  string
}

type NotifierConfig struct {
	Addr            string
	MQTTBroker      string
	MQTTClientID    string
	EventBufferSize string
}

func LoadGateway() GatewayConfig {
	return GatewayConfig{
		Addr:            getenv("GATEWAY_ADDR", ":8080"),
		DBPath:          getenv("DB_PATH", "./data/smarthotel.db"),
		MQTTBroker:      getenv("MQTT_BROKER", "tcp://localhost:1883"),
		MQTTClientID:    getenv("MQTT_CLIENT_ID", "smarthotel-gateway"),
		AuthServiceURL:  getenv("AUTH_SERVICE_URL", "http://localhost:8090"),
		AuthInternalKey: getenv("AUTH_INTERNAL_KEY", "dev-internal-key"),
	}
}

func LoadAuth() AuthConfig {
	return AuthConfig{
		Addr:           getenv("AUTH_ADDR", ":8090"),
		DBPath:         getenv("AUTH_DB_PATH", "./auth_data/auth.db"),
		InternalKey:    getenv("AUTH_INTERNAL_KEY", "dev-internal-key"),
		BootstrapAdmin: true,
		BootstrapUser:  getenv("AUTH_BOOTSTRAP_ADMIN_USER", "admin"),
		BootstrapPass:  getenv("AUTH_BOOTSTRAP_ADMIN_PASS", "admin123"),
	}
}

func LoadNotifier() NotifierConfig {
	return NotifierConfig{
		Addr:            getenv("NOTIFIER_ADDR", ":8081"),
		MQTTBroker:      getenv("MQTT_BROKER", "tcp://localhost:1883"),
		MQTTClientID:    getenv("MQTT_CLIENT_ID", "smarthotel-notifier"),
		EventBufferSize: getenv("EVENT_BUFFER_SIZE", "50"),
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
