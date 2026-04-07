package rafty

import (
	"fmt"
	"os"
)

// NullLogger is a logger which logs nothing.
type NullLogger struct{}

func (l *NullLogger) Tracef(format string, args ...interface{}) {}
func (l *NullLogger) Debugf(format string, args ...interface{}) {}
func (l *NullLogger) Infof(format string, args ...interface{})  {}
func (l *NullLogger) Warnf(format string, args ...interface{})  {}
func (l *NullLogger) Errorf(format string, args ...interface{}) {}

// StdLogger is a logger which logs on standard outputs.
type StdLogger struct{}

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
