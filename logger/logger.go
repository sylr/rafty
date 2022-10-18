// Package loggers contains default loggers for Rafty.
package logger

import (
	"fmt"
	"io"
	"os"

	"github.com/hashicorp/go-hclog"

	"sylr.dev/rafty/interfaces"
)

// HCLoggerWriter is a hclog.Logger wrapper which implemets io.Writer
type HCLoggerWriter struct {
	hclog.Logger
}

var _ io.Writer = (*HCLoggerWriter)(nil)

func (l *HCLoggerWriter) Write(p []byte) (n int, err error) {
	l.Debug(string(p))
	return len(p), nil
}

// NullLogger is a logger which logs nothing.
type NullLogger struct{}

var _ interfaces.Logger = (*NullLogger)(nil)

func (l *NullLogger) Tracef(format string, args ...interface{}) {

}

func (l *NullLogger) Debugf(format string, args ...interface{}) {

}

func (l *NullLogger) Infof(format string, args ...interface{}) {

}

func (l *NullLogger) Warnf(format string, args ...interface{}) {

}

func (l *NullLogger) Errorf(format string, args ...interface{}) {

}

// StdLogger is a logger which logs on standard outputs.
type StdLogger struct{}

var _ interfaces.Logger = (*StdLogger)(nil)

func (l *StdLogger) Tracef(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

func (l *StdLogger) Debugf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

func (l *StdLogger) Infof(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

func (l *StdLogger) Warnf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

func (l *StdLogger) Errorf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
