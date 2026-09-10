package access

import "time"

// RecoveryPollIntervalForTest exposes the derivation of a recovery poll
// interval from a device's horizon. It is the lane's own arithmetic rather
// than an exported API: the interval is never handed to a caller, it is only
// waited on inside the poll loop, so there is nothing else to assert it
// through.
func RecoveryPollIntervalForTest(l *Lane, horizon time.Duration) time.Duration {
	return l.recoveryPollInterval(horizon)
}
