package config

import (
	"context"
	"os"
	"strings"

	"github.com/pkg/errors"

	"github.com/Kaese72/cloud-connect/client/internal/logging"
	"github.com/spf13/viper"
)

type DatabaseConfig struct {
	Host     string `json:"host" mapstructure:"host"`
	Port     int    `json:"port" mapstructure:"port"`
	User     string `json:"user" mapstructure:"user"`
	Password string `json:"password" mapstructure:"password"`
	Database string `json:"database" mapstructure:"database"`
}

func (conf DatabaseConfig) Validate() error {
	if conf.Host == "" {
		return errors.New("must supply database host")
	}
	return nil
}

// AuthConfig configures verification of the "use" JWT issued by the
// authentication service - every endpoint on this service requires one,
// since enrolling activates an outbound internet tunnel.
type AuthConfig struct {
	UseTokenRSAPublicKeyPath string `json:"use-token-rsa-public-key-path" mapstructure:"use-token-rsa-public-key-path"`
}

func (conf AuthConfig) Validate() error {
	if conf.UseTokenRSAPublicKeyPath == "" {
		return errors.New("must supply auth use-token-rsa-public-key-path")
	}
	return nil
}

// ApplianceRegistryConfig points at appliance-registry's public base URL -
// reachable directly over the internet, independent of the cloud-connect
// tunnel (which does not exist until enrollment completes).
type ApplianceRegistryConfig struct {
	BaseURL string `json:"base-url" mapstructure:"base-url"`
}

func (conf ApplianceRegistryConfig) Validate() error {
	if conf.BaseURL == "" {
		return errors.New("must supply appliance-registry base-url")
	}
	return nil
}

// CloudConfig points at the cloud-hosted UI that starts an enrollment
// (cloud-ui's /enroll page), used to build the redirect URL returned by
// POST .../enrollment/start.
type CloudConfig struct {
	EnrollBaseURL string `json:"enroll-base-url" mapstructure:"enroll-base-url"`
}

func (conf CloudConfig) Validate() error {
	if conf.EnrollBaseURL == "" {
		return errors.New("must supply cloud enroll-base-url")
	}
	return nil
}

// TunnelConfig describes this appliance cluster's own local ingress -
// identical for every appliance today, so it stays local static config
// rather than anything appliance-registry issues. See cloud-connect's
// README.
type TunnelConfig struct {
	AllowedHostPattern string `json:"allowed-host-pattern" mapstructure:"allowed-host-pattern"`
	LocalIngress       string `json:"local-ingress" mapstructure:"local-ingress"`
}

func (conf TunnelConfig) Validate() error {
	if conf.AllowedHostPattern == "" {
		return errors.New("must supply tunnel allowed-host-pattern")
	}
	if conf.LocalIngress == "" {
		return errors.New("must supply tunnel local-ingress")
	}
	return nil
}

type Config struct {
	Database          DatabaseConfig          `json:"database" mapstructure:"database"`
	Auth              AuthConfig              `json:"auth" mapstructure:"auth"`
	ApplianceRegistry ApplianceRegistryConfig `json:"appliance-registry" mapstructure:"appliance-registry"`
	Cloud             CloudConfig             `json:"cloud" mapstructure:"cloud"`
	Tunnel            TunnelConfig            `json:"tunnel" mapstructure:"tunnel"`
	Port              int                     `json:"port" mapstructure:"port"`
}

func (conf Config) Validate() error {
	if err := conf.Database.Validate(); err != nil {
		return err
	}
	if err := conf.Auth.Validate(); err != nil {
		return err
	}
	if err := conf.ApplianceRegistry.Validate(); err != nil {
		return err
	}
	if err := conf.Cloud.Validate(); err != nil {
		return err
	}
	if err := conf.Tunnel.Validate(); err != nil {
		return err
	}
	return nil
}

var Loaded Config

func init() {
	// We have elected to not use AutomaticEnv() because of https://github.com/spf13/viper/issues/584
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	viper.BindEnv("database.host")
	viper.BindEnv("database.port")
	viper.BindEnv("database.user")
	viper.BindEnv("database.password")
	viper.BindEnv("database.database")
	viper.SetDefault("database.port", 3306)
	viper.SetDefault("database.database", "cloudconnectclient")

	viper.BindEnv("auth.use-token-rsa-public-key-path")

	viper.BindEnv("appliance-registry.base-url")

	viper.BindEnv("cloud.enroll-base-url")

	viper.BindEnv("tunnel.allowed-host-pattern")
	viper.BindEnv("tunnel.local-ingress")

	viper.BindEnv("logging.stdout")
	viper.SetDefault("logging.stdout", true)
	viper.BindEnv("logging.http.url")

	viper.BindEnv("port")
	viper.SetDefault("port", 8080)

	err := viper.Unmarshal(&Loaded)
	if err != nil {
		logging.Error(err.Error(), context.TODO())
		os.Exit(1)
	}
}
