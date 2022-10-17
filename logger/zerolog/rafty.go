package raftyzerolog

import (
	"github.com/rs/zerolog"

	"sylr.dev/rafty/interfaces"
)

var _ interfaces.Logger = (*RaftyLogger)(nil)

// RaftyLogger is a wrapper around zerolog.Logger that implements logger.Logger.
type RaftyLogger struct {
	zerolog.Logger
}

func (l *RaftyLogger) Tracef(format string, args ...interface{}) {
	l.Trace().Msgf(format, args...)
}

func (l *RaftyLogger) Debugf(format string, args ...interface{}) {
	l.Debug().Msgf(format, args...)
}

func (l *RaftyLogger) Infof(format string, args ...interface{}) {
	l.Info().Msgf(format, args...)
}

func (l *RaftyLogger) Warnf(format string, args ...interface{}) {
	l.Warn().Msgf(format, args...)
}

func (l *RaftyLogger) Errorf(format string, args ...interface{}) {
	l.Error().Msgf(format, args...)
}
