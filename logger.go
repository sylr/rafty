package rafty

import "github.com/hashicorp/go-hclog"

type Logger interface {
	Tracef(format string, fields ...interface{})
	Debugf(format string, fields ...interface{})
	Infof(format string, fields ...interface{})
	Warnf(format string, fields ...interface{})
	Errorf(format string, fields ...interface{})
}

type HCLoggerWriter struct {
	hclog.Logger
}

func (l *HCLoggerWriter) Write(p []byte) (n int, err error) {
	l.Debug(string(p))
	return len(p), nil
}
