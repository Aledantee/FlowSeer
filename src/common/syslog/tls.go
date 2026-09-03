package syslog

import (
	"crypto/tls"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func tlsConfiguration(config *tls.Config, server bool) (*tls.Config, error) {
	if config == nil {
		return nil, errs.Msg("syslog TLS requires configuration")
	}
	c := config.Clone()
	if c.MinVersion == 0 {
		c.MinVersion = tls.VersionTLS12
	}
	if c.MinVersion < tls.VersionTLS12 || c.Renegotiation != tls.RenegotiateNever {
		return nil, errs.Msg("syslog TLS requires TLS 1.2 or later without renegotiation")
	}
	if !server && c.InsecureSkipVerify {
		return nil, errs.Msg("syslog TLS requires certificate verification")
	}
	if server && len(c.Certificates) == 0 && c.GetCertificate == nil && c.GetConfigForClient == nil {
		return nil, errs.Msg("syslog TLS server certificate required")
	}
	return c, nil
}
