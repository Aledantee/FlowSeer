package testdata

import "fmt"

type Logger struct{}

func (Logger) Errorf(format string, args ...any) {}

func ReceiverMethod(log Logger) {
	log.Errorf("unrelated receiver method: %s", fmt.Sprintf("value"))
}
