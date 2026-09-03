// Copyright 2025 Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

package config

import (
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	DaemonLogFilePath        string        `envconfig:"NORTHRAYS_DAEMON_LOG_FILE_PATH"`
	DaemonLogMaxSizeMB       int           `envconfig:"NORTHRAYS_DAEMON_LOG_MAX_SIZE_MB" default:"100" validate:"min=1"`
	DaemonLogMaxBackups      int           `envconfig:"NORTHRAYS_DAEMON_LOG_MAX_BACKUPS" default:"5" validate:"min=1"`
	DaemonLogMaxAgeDays      int           `envconfig:"NORTHRAYS_DAEMON_LOG_MAX_AGE_DAYS" default:"14" validate:"min=1"`
	DaemonLogCompress        bool          `envconfig:"NORTHRAYS_DAEMON_LOG_COMPRESS" default:"true"`
	UserHomeAsWorkDir        bool          `envconfig:"NORTHRAYS_USER_HOME_AS_WORKDIR"`
	SandboxId                string        `envconfig:"NORTHRAYS_SANDBOX_ID" validate:"required"`
	OtelEndpoint             *string       `envconfig:"NORTHRAYS_OTEL_ENDPOINT"`
	TerminationCheckInterval time.Duration `envconfig:"NORTHRAYS_TERMINATION_CHECK_INTERVAL" default:"100ms" validate:"min_duration=1ms"`
	TerminationGracePeriod   time.Duration `envconfig:"NORTHRAYS_TERMINATION_GRACE_PERIOD" default:"5s" validate:"min_duration=1s"`
	RecordingsDir            string        `envconfig:"NORTHRAYS_RECORDINGS_DIR"`
	OrganizationId           *string       `envconfig:"NORTHRAYS_ORGANIZATION_ID"`
	RegionId                 *string       `envconfig:"NORTHRAYS_REGION_ID"`
	Snapshot                 *string       `envconfig:"NORTHRAYS_SANDBOX_SNAPSHOT"`
}

var defaultDaemonLogFilePath = "/tmp/northrays-daemon.log"

var config *Config

func GetConfig() (*Config, error) {
	if config != nil {
		return config, nil
	}

	config = &Config{}

	err := envconfig.Process("", config)
	if err != nil {
		return nil, err
	}

	var validate = validator.New()

	// Register a custom tag "min_duration" that accepts a duration string like "1ms"
	err = validate.RegisterValidation("min_duration", func(fl validator.FieldLevel) bool {
		min, err := time.ParseDuration(fl.Param())
		if err != nil {
			return false
		}
		d, ok := fl.Field().Interface().(time.Duration)
		if !ok {
			return false
		}
		return d >= min
	})
	if err != nil {
		return nil, err
	}

	err = validate.Struct(config)
	if err != nil {
		return nil, err
	}

	if config.DaemonLogFilePath == "" {
		config.DaemonLogFilePath = defaultDaemonLogFilePath
	}

	return config, nil
}
