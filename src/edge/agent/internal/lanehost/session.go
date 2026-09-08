package lanehost

import (
	"context"
	"net"
	"strconv"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

// ErrCodeSession identifies a device session that could not be opened from
// the credential central handed out for it.
var ErrCodeSession = errs.NewCode("agent/device-session")

// Endpoint is where one device answers: its address, and the SNMP and SSH
// ports if they are not the defaults.
type Endpoint struct {
	// Address is the host or IP, with no port.
	Address string
	// SNMPPort and SSHPort default to 161 and 22 when zero.
	SNMPPort int
	SSHPort  int
}

func (e Endpoint) snmpTarget() string { return joinPort(e.Address, e.SNMPPort, 161) }
func (e Endpoint) sshTarget() string  { return joinPort(e.Address, e.SSHPort, 22) }

// joinPort renders host:port, taking the default when none was configured.
func joinPort(address string, port, fallback int) string {
	if port == 0 {
		port = fallback
	}
	return net.JoinHostPort(address, strconv.Itoa(port))
}

// OpenSNMP builds the SNMP session factory for a device at endpoint.
func OpenSNMP(endpoint Endpoint) func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
	return func(ctx context.Context, cred *edgev1.DeviceCredential) (access.SNMPSession, error) {
		usm, err := usmFrom(cred.GetTypedMaterial().GetSnmpV3())
		if err != nil {
			return access.SNMPSession{}, err
		}
		session, err := snmp.NewSession(ctx, endpoint.snmpTarget(), snmp.V3, snmp.WithUSM(usm))
		if err != nil {
			return access.SNMPSession{}, errs.From(err).Code(ErrCodeSession).
				Attr("target", endpoint.snmpTarget()).Msg("open snmp session")
		}
		return access.SNMPSession{Session: session, Close: session.Close}, nil
	}
}

// usmFrom maps the credential central delivered onto the SNMPv3 user.
//
// Only SNMPv3. There is no community-string path: a v2c community is a
// bearer secret that every read puts on the wire in the clear, and this
// agent acquires a fresh credential per operation precisely so that the
// secret's lifetime is bounded.
func usmFrom(credential *credentialv1.SnmpV3Credential) (snmp.USMConfig, error) {
	if credential == nil {
		return snmp.USMConfig{}, errs.New().Code(ErrCodeSession).
			Msg("credential carries no SNMPv3 material")
	}
	auth, err := authProtocol(credential.GetAuthProtocol())
	if err != nil {
		return snmp.USMConfig{}, err
	}
	priv, err := privProtocol(credential.GetPrivProtocol())
	if err != nil {
		return snmp.USMConfig{}, err
	}
	usm := snmp.USMConfig{
		Username:       credential.GetUser(),
		AuthProtocol:   auth,
		AuthPassphrase: credential.GetAuthPassphrase(),
		PrivProtocol:   priv,
		PrivPassphrase: credential.GetPrivPassphrase(),
	}
	if err := usm.Validate(); err != nil {
		return snmp.USMConfig{}, errs.From(err).Code(ErrCodeSession).
			Msg("credential does not describe a usable SNMPv3 user")
	}
	return usm, nil
}

// authProtocol maps the schema's enum onto the library's.
//
// An unspecified protocol is refused rather than mapped to none. The zero
// value of an enum is what an unset field looks like, and treating it as
// noAuth would turn a credential central failed to populate into an
// unauthenticated read of a device.
func authProtocol(protocol credentialv1.SnmpAuthProtocol) (snmp.AuthProtocol, error) {
	switch protocol {
	case credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA1:
		return snmp.AuthSHA, nil
	case credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA224:
		return snmp.AuthSHA224, nil
	case credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA256:
		return snmp.AuthSHA256, nil
	case credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA384:
		return snmp.AuthSHA384, nil
	case credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA512:
		return snmp.AuthSHA512, nil
	case credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_UNSPECIFIED:
		return snmp.AuthProtocolNone, errs.New().Code(ErrCodeSession).
			Msg("credential names no SNMPv3 authentication protocol")
	default:
		return snmp.AuthProtocolNone, errs.New().Code(ErrCodeSession).
			Attr("protocol", protocol.String()).Msg("unknown SNMPv3 authentication protocol")
	}
}

// privProtocol maps the schema's enum onto the library's, refusing an
// unspecified one for the same reason authProtocol does.
func privProtocol(protocol credentialv1.SnmpPrivProtocol) (snmp.PrivProtocol, error) {
	switch protocol {
	case credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES128:
		return snmp.PrivAES, nil
	case credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES192:
		return snmp.PrivAES192, nil
	case credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES256:
		return snmp.PrivAES256, nil
	case credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_UNSPECIFIED:
		// Named as the device's configuration rather than the credential's,
		// because that is what an operator meeting this has to change. Every
		// field of SnmpV3Credential is required, so this system manages
		// devices at authPriv only; a device configured authNoPriv produces
		// a credential central populated correctly and this call refuses,
		// and "no privacy protocol" would send the operator to look at
		// credential delivery, which is not where the problem is.
		return snmp.PrivProtocolNone, errs.New().Code(ErrCodeSession).
			Msg("this device is managed at a security level below authPriv, which is not supported; check its SNMPv3 configuration")
	default:
		return snmp.PrivProtocolNone, errs.New().Code(ErrCodeSession).
			Attr("protocol", protocol.String()).Msg("unknown SNMPv3 privacy protocol")
	}
}

// OpenShell builds the shell session factory for a device at endpoint.
//
// The host key is pinned from the digest the credential response carries,
// and an empty digest is refused. There is deliberately no path here that
// sets ssh.Options.InsecureIgnoreHostKey: an unpinned SSH session to a
// network device is one where anything that can answer on the address can
// take the credential this call is about to send it, and "the pin was empty"
// must never be the same thing as "any host will do". The SSH library refuses
// an empty pin too — this refuses it first, so the failure names the
// credential rather than the transport.
func OpenShell(endpoint Endpoint) func(context.Context, *edgev1.DeviceCredential, string) (access.ShellSession, error) {
	return func(ctx context.Context, cred *edgev1.DeviceCredential, hostKeySHA256 string) (access.ShellSession, error) {
		shell := cred.GetTypedMaterial().GetShell()
		if shell == nil {
			return access.ShellSession{}, errs.New().Code(ErrCodeSession).
				Msg("credential carries no shell material")
		}
		if hostKeySHA256 == "" {
			return access.ShellSession{}, errs.New().Code(ErrCodeSession).
				Attr("target", endpoint.sshTarget()).
				Msg("credential carries no host key digest; an unpinned shell session is never opened")
		}

		session, err := ssh.Dial(ctx, endpoint.sshTarget(), ssh.Options{
			Username:      shell.GetUsername(),
			Password:      shell.GetPassword(),
			HostKeySHA256: hostKeySHA256,
		})
		if err != nil {
			return access.ShellSession{}, errs.From(err).Code(ErrCodeSession).
				Attr("target", endpoint.sshTarget()).Msg("open shell session")
		}

		adapter, err := access.NewFastIronShell(ctx, session, shell.GetEnablePassword())
		if err != nil {
			_ = session.Close()
			return access.ShellSession{}, errs.From(err).Code(ErrCodeSession).
				Attr("target", endpoint.sshTarget()).Msg("log in to the device shell")
		}
		return access.ShellSession{Adapter: adapter, Close: session.Close}, nil
	}
}
