package raftyzerolog

import (
	"github.com/rs/zerolog"

	"sylr.dev/rafty"
)

var _ rafty.Logger = (*RaftyLogger)(nil)

// Logger is a wrapper around zerolog.Logger that implements hclog.Logger.
type RaftyLogger struct {
	zerolog.Logger
}

func (l *RaftyLogger) Tracef(format string, args ...interface{}) {
	l.Logger.Trace().Msgf(format, args...)
}

func (l *RaftyLogger) Debugf(format string, args ...interface{}) {
	l.Logger.Debug().Msgf(format, args...)
}

func (l *RaftyLogger) Infof(format string, args ...interface{}) {
	l.Logger.Info().Msgf(format, args...)
}

func (l *RaftyLogger) Warnf(format string, args ...interface{}) {
	l.Logger.Warn().Msgf(format, args...)
}

func (l *RaftyLogger) Errorf(format string, args ...interface{}) {
	l.Logger.Error().Msgf(format, args...)
}
